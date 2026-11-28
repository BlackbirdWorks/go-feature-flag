package valkeysegment

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strconv"
	"strings"

	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// cmdGet is the name of the command that reads a version key.
const (
	cmdGet   = "GET"
	cmdSScan = "SSCAN"
)

// ScanCount is the COUNT hint of each SSCAN page of [Provider.Members].
const ScanCount = 1000

// MaxScanPages is the most pages that one scan may take: a store that keeps answering pages (a cursor loop) fails the
// scan instead of running for ever. At a COUNT of 1000 it is a set of more than a billion members.
const MaxScanPages = 1 << 20

// versionPrefix is put before a value that was read from a version key, so that no stored value can be equal to
// [segment.AbsentVersion], the token of a version key that does not exist.
const versionPrefix = "v:"

var (
	_ segment.SnapshotSource = (*Provider)(nil)
	_ segment.Versioner      = (*Provider)(nil)
)

// isSnapshotCommand tells whether a command is one that only a snapshot read sends: SSCAN and GET. [Dial] never lets
// the client send them to a replica, because a scan cursor belongs to one node and a version must come from the node
// that has the members.
func isSnapshotCommand(cmd valkey.Completed) bool { return isSnapshotWords(cmd.Commands()) }

// isSnapshotWords is isSnapshotCommand on the words of a command.
func isSnapshotWords(words []string) bool {
	return len(words) > 0 && (words[0] == cmdSScan || words[0] == cmdGet)
}

// run sends one command under the timeout of the provider (a pinned read does not matter here: the client never sends
// SSCAN or GET to a replica; a cluster migration of the slot can fail a refresh, the holder keeps the last known good
// set and the next poll succeeds) and returns its result. The outcome feeds the health of the
// provider, with the classes of a lookup.
func (p *Provider) run(ctx context.Context, build func(valkey.Builder) valkey.Completed) (valkey.ValkeyResult, error) {
	if !p.admit() {
		return valkey.ValkeyResult{}, ErrClosed
	}
	defer p.inflight.Done()

	callCtx, cancel := p.callContext(ctx)
	defer cancel()

	results := p.client.DoMulti(callCtx, build(p.client.B()))
	if len(results) != 1 {
		p.record(ClassProtocol)

		return valkey.ValkeyResult{}, fmt.Errorf("%w: %d replies for one command", ErrProtocol, len(results))
	}

	err := results[0].Error()
	if err != nil && !errors.Is(err, valkey.Nil) {
		res := result{ctx: ctx, callCtx: callCtx}
		p.record(res.classify(err))

		if isWrongType(err) {
			// the store answered about this set: the key does not hold a set. A consumer tells that from a store that fails.
			return results[0], fmt.Errorf("%w: valkey segment: %w", ErrServer, err)
		}

		return results[0], fmt.Errorf("valkey segment: %w", err)
	}

	p.record(ClassNone)

	if err != nil {
		return results[0], fmt.Errorf("valkey segment: %w", err)
	}

	return results[0], nil
}

func (p *Provider) segmentConfigOf(name string) (*segmentConfig, error) {
	cfg, ok := p.segs[name]
	if !ok {
		return nil, fmt.Errorf("valkey segment: %q is not configured: %w", name, segment.ErrSegmentMissing)
	}

	return cfg, nil
}

// Version implements [segment.Versioner]: the value of the version key of the segment (GET), prefixed with "v:". A
// version key that does not exist is [segment.AbsentVersion], which no stored value can be equal to. A segment with no
// version key has no version: the error wraps [segment.ErrNoVersion].
func (p *Provider) Version(ctx context.Context, name string) (string, error) {
	cfg, err := p.segmentConfigOf(name)
	if err != nil {
		return "", err
	}

	if cfg.versionKey == "" {
		return "", fmt.Errorf("%w: %q", ErrNoVersionKey, name)
	}

	res, err := p.run(ctx, func(b valkey.Builder) valkey.Completed { return b.Get().Key(cfg.versionKey).Build() })
	if errors.Is(err, valkey.Nil) {
		return segment.AbsentVersion, nil
	}

	if err != nil {
		return "", err
	}

	token, err := res.ToString()
	if err != nil {
		p.record(ClassProtocol)

		return "", fmt.Errorf("%w: version of %q: %w", ErrProtocol, name, err)
	}

	return versionPrefix + token, nil
}

// Members implements [segment.SnapshotSource] for the set layout: SSCAN pages of ScanCount members, streamed as they
// arrive. The client never sends SSCAN to a replica (see [Dial]). SSCAN may repeat a member, which the consumer's set
// absorbs. An empty set does not exist in the store, so a segment with no member is reported as
// [segment.ErrSegmentMissing], whatever the MissingKey of the provider: what a missing segment means for the
// snapshot is the policy of the holder, not of the store. A reply that is not a well formed page (a cursor that is not
// a number, members that are not strings, a cursor that comes back, too many pages) is an error that wraps
// [ErrProtocol], never the end of the scan.
//
// The keys layout is refused with [ErrSnapshotLayout]: its members are separate keys, so the only way to list them is
// SCAN with a MATCH over the whole keyspace of every node, which reads every key of the database, not only the
// segment, and on a cluster reaches only one node. That is too slow and not safe for a periodic pull.
func (p *Provider) Members(ctx context.Context, name string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		cfg, err := p.segmentConfigOf(name)
		if err != nil {
			yield("", err)

			return
		}

		if cfg.layout != LayoutSet {
			yield("", fmt.Errorf("%w: %q", ErrSnapshotLayout, name))

			return
		}

		seen, ok := p.scanSet(ctx, name, cfg.key, yield)
		if ok && !seen {
			yield("", fmt.Errorf("valkey segment: %q: %w", name, segment.ErrSegmentMissing))
		}
	}
}

// scanSet yields the pages of one SSCAN. It reports whether it saw a member, and false as the second value when it
// stopped on an error or because the consumer stopped.
func (p *Provider) scanSet(ctx context.Context, name, key string, yield func(string, error) bool) (bool, bool) {
	var cursor uint64

	seen := false

	for pages := 0; ; pages++ {
		if pages >= MaxScanPages {
			return seen, p.badPage(name, errTooManyPages, yield)
		}

		res, err := p.run(ctx, func(b valkey.Builder) valkey.Completed {
			return b.Sscan().Key(key).Cursor(cursor).Count(ScanCount).Build()
		})
		if err != nil {
			yield("", err)

			return seen, false
		}

		next, members, err := scanPage(res)
		if err != nil {
			return seen, p.badPage(name, err, yield)
		}

		for _, member := range members {
			seen = true

			if !yield(member, nil) {
				return seen, false
			}
		}

		if next == 0 {
			return seen, true
		}

		if next == cursor {
			return seen, p.badPage(name, errCursorLoop, yield)
		}

		cursor = next
	}
}

// badPage reports a page that is not well formed to the health of the provider and to the consumer.
func (p *Provider) badPage(name string, cause error, yield func(string, error) bool) bool {
	p.record(ClassProtocol)
	yield("", fmt.Errorf("%w: %q: %w", ErrProtocol, name, cause))

	return false
}

// scanReplyLen is the length of the reply of SSCAN: the cursor and the list.
const scanReplyLen = 2

var (
	errScanShape    = errors.New("a scan reply is not a cursor and a list of strings")
	errScanCursor   = errors.New("the cursor of a scan reply is not a number")
	errCursorLoop   = errors.New("the scan returned the cursor that it was sent")
	errTooManyPages = errors.New("the scan took more pages than the limit")
)

// scanPage reads the reply of SSCAN strictly, without the coercions of AsScanEntry (it turns a member that is not a
// string into an empty one): a cursor that is a non-empty string of digits, and a list of strings.
func scanPage(res valkey.ValkeyResult) (uint64, []string, error) {
	msgs, err := res.ToArray()
	if err != nil || len(msgs) != scanReplyLen {
		return 0, nil, errScanShape
	}

	text, err := msgs[0].ToString()
	if err != nil {
		return 0, nil, errScanShape
	}

	cursor, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0, nil, errScanCursor
	}

	items, err := msgs[1].ToArray()
	if err != nil {
		return 0, nil, errScanShape
	}

	members := make([]string, 0, len(items))

	for _, item := range items {
		member, itemErr := item.ToString()
		if itemErr != nil {
			return 0, nil, errScanShape
		}

		members = append(members, member)
	}

	return cursor, members, nil
}

// isWrongType reports whether the store answered that the key holds another kind of value: an error about one set, not
// of the store (a loading store or a cluster that is down answers other errors).
func isWrongType(err error) bool {
	verr, ok := valkey.IsValkeyErr(err)

	return ok && strings.HasPrefix(verr.Error(), "WRONGTYPE")
}
