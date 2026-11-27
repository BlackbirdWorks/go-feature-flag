package valkeysegment

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// ScanCount is the COUNT hint of each SSCAN page of [Provider.Members].
const ScanCount = 1000

var (
	_ segment.SnapshotSource = (*Provider)(nil)
	_ segment.Versioner      = (*Provider)(nil)
)

// run sends one command under the timeout of the provider and returns its result.
func (p *Provider) run(ctx context.Context, build func(valkey.Builder) valkey.Completed) (valkey.ValkeyResult, error) {
	if !p.admit() {
		return valkey.ValkeyResult{}, ErrClosed
	}
	defer p.inflight.Done()

	callCtx, cancel := p.callContext(ctx)
	defer cancel()

	results := p.client.DoMulti(callCtx, build(p.client.B()))
	if len(results) != 1 {
		return valkey.ValkeyResult{}, fmt.Errorf("%w: %d replies for one command", ErrProtocol, len(results))
	}

	err := results[0].Error()
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

	res, err := p.run(ctx, func(b valkey.Builder) valkey.Completed { return b.Get().Key(cfg.versionKey).Build() })
	if errors.Is(err, valkey.Nil) {
		return segment.AbsentVersion, nil
	}

	if err != nil {
		return "", err
	}

	token, err := res.ToString()
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
			yield("", fmt.Errorf("valkey segment: %q: %w", name, segment.ErrSegmentMissing))
		}
	}
}

// scanSet yields the pages of one SSCAN. It reports whether it saw a member, and false as the second value when it
// stopped on an error or because the consumer stopped.
func (p *Provider) scanSet(ctx context.Context, name, key string, yield func(string, error) bool) (bool, bool) {
	var cursor uint64

	seen := false

	for {
		res, err := p.run(ctx, func(b valkey.Builder) valkey.Completed {
			return b.Sscan().Key(key).Cursor(cursor).Count(ScanCount).Build()
		})
		if err != nil {
			yield("", err)

			return seen, false
		}

		entry, err := res.AsScanEntry()
		if err != nil {
			yield("", fmt.Errorf("%w: %q: %w", ErrProtocol, name, err))

			return seen, false
		}

		for _, member := range entry.Elements {
			seen = true

			if !yield(member, nil) {
				return seen, false
			}
		}

		if entry.Cursor == 0 {
			return seen, true
		}

		cursor = entry.Cursor
	}
}
