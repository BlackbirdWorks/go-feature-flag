package redissegment

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"github.com/redis/go-redis/v9"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// ScanCount is the COUNT hint of each SSCAN page of [Provider.Members].
const ScanCount = 1000

var (
	_ segment.SnapshotSource = (*Provider)(nil)
	_ segment.Versioner      = (*Provider)(nil)
)

// run sends one command in its own pipeline, under the timeout of the provider, and returns its command. A reply of
// nil is the error redis.Nil of the command.
func (p *Provider) run(ctx context.Context, args ...any) (*redis.Cmd, error) {
	if !p.admit() {
		return nil, ErrClosed
	}
	defer p.inflight.Done()

	callCtx, cancel := p.callContext(ctx)
	defer cancel()

	pipe := p.client.Pipeline()
	cmd := pipe.Do(callCtx, args...)

	_, execErr := pipe.Exec(callCtx)

	err := cmd.Err()
	if err == nil && execErr != nil && !errors.Is(execErr, redis.Nil) {
		err = execErr
	}

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

// Version implements [segment.Versioner]: the value of the version key of the segment (GET). A missing key is
// [segment.AbsentVersion].
func (p *Provider) Version(ctx context.Context, name string) (string, error) {
	cfg, err := p.segmentConfigOf(name)
	if err != nil {
		return "", err
	}

	if cfg.versionKey == "" {
		return "", fmt.Errorf("%w: %q", ErrNoVersionKey, name)
	}

	cmd, err := p.run(ctx, "GET", cfg.versionKey)
	if errors.Is(err, redis.Nil) {
		return segment.AbsentVersion, nil
	}

	if err != nil {
		return "", err
	}

	token, err := cmd.Text()
	if err != nil {
		return "", fmt.Errorf("%w: version of %q: %w", ErrProtocol, name, err)
	}

	return token, nil
}

// Members implements [segment.SnapshotSource] for the set layout: SSCAN pages of ScanCount members, streamed as they
// arrive. SSCAN may repeat a member, which the consumer's set absorbs. An empty set does not exist in the store, so a
// segment with no member is reported as [segment.ErrSegmentMissing], whatever the MissingKey of the provider: what a
// missing segment means for the snapshot is the policy of the holder, not of the store.
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
			yield("", fmt.Errorf("redis segment: %q: %w", name, segment.ErrSegmentMissing))
		}
	}
}

// scanSet yields the pages of one SSCAN. It reports whether it saw a member, and false as the second value when it
// stopped on an error or because the consumer stopped.
func (p *Provider) scanSet(ctx context.Context, name, key string, yield func(string, error) bool) (bool, bool) {
	cursor, seen := "0", false

	for cursor != "" {
		cmd, err := p.run(ctx, "SSCAN", key, cursor, "COUNT", ScanCount)
		if err != nil {
			yield("", err)

			return seen, false
		}

		next, members, err := scanPage(cmd)
		if err != nil {
			yield("", fmt.Errorf("%w: %q: %w", ErrProtocol, name, err))

			return seen, false
		}

		for _, member := range members {
			seen = true

			if !yield(member, nil) {
				return seen, false
			}
		}

		cursor = next
		if cursor == "0" {
			cursor = ""
		}
	}

	return seen, true
}

// scanReplyLen is the length of the reply of SSCAN: the cursor and the list.
const scanReplyLen = 2

var errScanShape = errors.New("a scan reply is not a cursor and a list")

// scanPage reads the reply of SSCAN: the next cursor and the members.
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
