package redissegment

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strconv"

	"github.com/redis/go-redis/v9"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
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

// masterFinder is implemented by the cluster client of go-redis: it returns the client of the primary node of a key.
type masterFinder interface {
	MasterForKey(ctx context.Context, key string) (*redis.Client, error)
}

// primaryOf returns the client that serves the key of a snapshot read. A cluster client may route a read to a replica
// (RouteRandomly, RouteByLatency, ReadOnly), and a scan cursor belongs to one node, so every read of a snapshot goes to
// the primary of the slot of its key. Any other client is used as it is.
//
//nolint:ireturn // the Client interface is what the provider sends its commands through
func (p *Provider) primaryOf(ctx context.Context, key string) (Client, error) {
	finder, ok := p.client.(masterFinder)
	if !ok {
		return p.client, nil
	}

	callCtx, cancel := p.callContext(ctx)
	defer cancel()

	node, err := finder.MasterForKey(callCtx, key)
	if err != nil {
		p.recordError(ctx, callCtx, err)

		return nil, fmt.Errorf("redis segment: finding the primary of a key: %w", err)
	}

	return node, nil
}

// recordError gives the outcome of a failed snapshot call to the health of the provider, with the class of a lookup.
func (p *Provider) recordError(ctx, callCtx context.Context, err error) {
	res := result{ctx: ctx, callCtx: callCtx}

	p.record(res.classify(err))
}

// run sends one command in its own pipeline on the client, under the timeout of the provider, and returns its
// command. A reply of nil is the error redis.Nil of the command, and counts as a call that worked.
func (p *Provider) run(ctx context.Context, client Client, args ...any) (*redis.Cmd, error) {
	if !p.admit() {
		return nil, ErrClosed
	}
	defer p.inflight.Done()

	callCtx, cancel := p.callContext(ctx)
	defer cancel()

	pipe := client.Pipeline()
	cmd := pipe.Do(callCtx, args...)

	_, execErr := pipe.Exec(callCtx)

	err := cmd.Err()
	if err == nil && execErr != nil && !errors.Is(execErr, redis.Nil) {
		err = execErr
	}

	if err != nil && !errors.Is(err, redis.Nil) {
		p.recordError(ctx, callCtx, err)

		return cmd, fmt.Errorf("redis segment: %w", err)
	}

	p.record(ClassNone)

	if err != nil {
		return cmd, fmt.Errorf("redis segment: %w", err)
	}

	return cmd, nil
}

func (p *Provider) segmentConfigOf(name string) (*segmentConfig, error) {
	cfg, ok := p.segs[name]
	if !ok {
		return nil, fmt.Errorf("redis segment: %q is not configured: %w", name, segment.ErrSegmentMissing)
	}

	return cfg, nil
}

// Version implements [segment.Versioner]: the value of the version key of the segment (GET), read from the primary of
// its slot and prefixed with "v:". A version key that does not exist is [segment.AbsentVersion], which no stored value
// can be equal to. A segment with no version key has no version: the error wraps [segment.ErrNoVersion].
func (p *Provider) Version(ctx context.Context, name string) (string, error) {
	cfg, err := p.segmentConfigOf(name)
	if err != nil {
		return "", err
	}

	if cfg.versionKey == "" {
		return "", fmt.Errorf("%w: %q", ErrNoVersionKey, name)
	}

	node, err := p.primaryOf(ctx, cfg.versionKey)
	if err != nil {
		return "", err
	}

	cmd, err := p.run(ctx, node, "GET", cfg.versionKey)
	if errors.Is(err, redis.Nil) {
		return segment.AbsentVersion, nil
	}

	if err != nil {
		return "", err
	}

	token, err := cmd.Text()
	if err != nil {
		p.record(ClassProtocol)

		return "", fmt.Errorf("%w: version of %q: %w", ErrProtocol, name, err)
	}

	return versionPrefix + token, nil
}

// Members implements [segment.SnapshotSource] for the set layout: SSCAN pages of ScanCount members, all of them read
// from the primary of the slot of the set (a cursor belongs to one node), streamed as they arrive. SSCAN may repeat a
// member, which the consumer's set absorbs. An empty set does not exist in the store, so a segment with no member is
// reported as [segment.ErrSegmentMissing], whatever the MissingKey of the provider: what a missing segment means for
// the snapshot is the policy of the holder, not of the store. A reply that is not a well formed page (a cursor that is
// not a number, members that are not strings, a cursor that comes back, too many pages) is an error that wraps
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

		node, err := p.primaryOf(ctx, cfg.key)
		if err != nil {
			yield("", err)

			return
		}

		seen, ok := p.scanSet(ctx, node, name, cfg.key, yield)
		if ok && !seen {
			yield("", fmt.Errorf("redis segment: %q: %w", name, segment.ErrSegmentMissing))
		}
	}
}

// scanSet yields the pages of one SSCAN from one node. It reports whether it saw a member, and false as the second
// value when it stopped on an error or because the consumer stopped.
func (p *Provider) scanSet(
	ctx context.Context, node Client, name, key string, yield func(string, error) bool,
) (bool, bool) {
	cursor, seen := "0", false

	for pages := 0; ; pages++ {
		if pages >= MaxScanPages {
			return seen, p.badPage(name, errTooManyPages, yield)
		}

		cmd, err := p.run(ctx, node, "SSCAN", key, cursor, "COUNT", ScanCount)
		if err != nil {
			yield("", err)

			return seen, false
		}

		next, members, err := scanPage(cmd)
		if err != nil {
			return seen, p.badPage(name, err, yield)
		}

		for _, member := range members {
			seen = true

			if !yield(member, nil) {
				return seen, false
			}
		}

		if next == "0" {
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

const (
	// scanReplyLen is the length of the reply of SSCAN: the cursor and the list.
	scanReplyLen = 2
)

var (
	errScanShape    = errors.New("a scan reply is not a cursor and a list of strings")
	errScanCursor   = errors.New("the cursor of a scan reply is not a number")
	errCursorLoop   = errors.New("the scan returned the cursor that it was sent")
	errTooManyPages = errors.New("the scan took more pages than the limit")
)

// scanPage reads the reply of SSCAN strictly: a cursor that is a non-empty string of digits, and a list of strings.
func scanPage(cmd *redis.Cmd) (string, []string, error) {
	val, err := cmd.Slice()
	if err != nil {
		return "", nil, fmt.Errorf("reading the scan reply: %w", err)
	}

	if len(val) != scanReplyLen {
		return "", nil, errScanShape
	}

	cursor, isText := val[0].(string)
	items, isList := val[1].([]any)

	if !isText || !isList {
		return "", nil, errScanShape
	}

	_, parseErr := strconv.ParseUint(cursor, 10, 64)
	if parseErr != nil {
		return "", nil, errScanCursor
	}

	members := make([]string, 0, len(items))

	for _, item := range items {
		member, ok := item.(string)
		if !ok {
			return "", nil, errScanShape
		}

		members = append(members, member)
	}

	return cursor, members, nil
}
