//go:build docker

package segmentadmin_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

const (
	prefixOld  = "old"
	prefixNew  = "new"
	e2eIDs     = 200_000
	e2eReaders = 4
	maxStall   = 2 * time.Second
	segEnc     = "semaphore:segment:{enc}"
	segSwap    = "semaphore:segment:{swap}"
	segCut     = "semaphore:segment:{cut}"
	segEdit    = "semaphore:segment:{edit}"
	segFlags   = "semaphore:segment:flags:"
)

func e2eSegments() map[string]string {
	return map[string]string{"enc": segEnc, "swap": segSwap, "cut": segCut, "edit": segEdit, "flags": segFlags}
}

func e2eTarget(key string, layout redissegment.Layout) *segmentadmin.Target {
	return &segmentadmin.Target{
		Key: key, Layout: layout, VersionKey: versionKeyOf(key), Normalize: resolver.NormalizeTrimLower,
	}
}

// firstError keeps the first error that a goroutine meets, for the test goroutine to assert.
type firstError struct {
	err error
	mu  sync.Mutex
}

func (f *firstError) set(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err == nil {
		f.err = err
	}
}

func (f *firstError) get() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.err
}

func TestE2E(t *testing.T) {
	t.Parallel()

	for _, maker := range makers() {
		t.Run(maker.name, func(t *testing.T) {
			t.Parallel()

			srv := maker.start(t)
			back := maker.build(t, srv, e2eSegments())

			require.Eventually(t, func() bool { return back.store.Ping(t.Context()) == nil }, 30*time.Second,
				100*time.Millisecond, "the store does not answer")

			t.Run("the relay finds what was imported, byte for byte", func(t *testing.T) {
				t.Parallel()
				e2eEncoding(t, back)
			})
			t.Run("readers see the old set or the new set, never a part", func(t *testing.T) {
				t.Parallel()
				e2eAtomic(t, back)
			})
			t.Run("an interrupted import leaves the live set", func(t *testing.T) {
				t.Parallel()
				e2eInterrupted(t, back)
			})
			t.Run("add, remove and stat", func(t *testing.T) {
				t.Parallel()
				e2eEdits(t, back)
			})
		})
	}
}

func e2eEncoding(t *testing.T, back *backend) {
	t.Helper()

	ctx := t.Context()
	target := e2eTarget(segEnc, redissegment.LayoutSet)

	rep, err := segmentadmin.Import(ctx, back.store, target,
		newReader(t, stringsReader("  Alice@Example.COM \nBOB\nÜnï\nbob\n\n")), segmentadmin.ImportOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(5), rep.Read)
	assert.Equal(t, int64(3), rep.Added)
	assert.Equal(t, int64(1), rep.Duplicates)
	assert.Equal(t, segmentadmin.Rejections{Empty: 1}, rep.Rejected)

	got, err := states(ctx, back.provider, "enc", "alice@example.com", " BOB ", "ÜNï ", "carol")
	require.NoError(t, err)
	assert.Equal(t, []segment.State{segment.Member, segment.Member, segment.Member, segment.NotMember}, got)

	raw, err := states(ctx, back.relay, "enc", "alice@example.com", "bob", "ünï", "  Alice@Example.COM ")
	require.NoError(t, err)
	assert.Equal(t, []segment.State{segment.Member, segment.Member, segment.Member, segment.NotMember}, raw,
		"the store holds the normalised ids, as the lookup of the relay asks for them")

	stat, err := segmentadmin.Stat(ctx, back.store, target, 0)
	require.NoError(t, err)
	assert.Equal(t, segmentadmin.Stats{Exists: true, Count: 3, Version: rep.Version, HasVersion: true}, *stat)

	for _, key := range []string{segEnc + ":import:new", segEnc + ":import:old"} {
		exists, _, ttlErr := back.store.KeyTTL(ctx, key)
		require.NoError(t, ttlErr)
		assert.False(t, exists, "%s is removed", key)
	}
}

// e2eAtomic replaces a set of e2eIDs ids by another while readers ask for ids of both, and a pinger measures the
// longest wait of a command.
func e2eAtomic(t *testing.T, back *backend) {
	t.Helper()

	ctx := t.Context()
	target := e2eTarget(segSwap, redissegment.LayoutSet)

	_, err := segmentadmin.Import(ctx, back.store, target, newReader(t, &idSource{prefix: prefixOld, n: e2eIDs}),
		segmentadmin.ImportOptions{})
	require.NoError(t, err)

	probe := []string{"old-0", "old-1", "old-99999", "old-199999", "new-0", "new-1", "new-99999", "new-199999"}
	allOld := []segment.State{
		segment.Member, segment.Member, segment.Member, segment.Member,
		segment.NotMember, segment.NotMember, segment.NotMember, segment.NotMember,
	}
	allNew := []segment.State{
		segment.NotMember, segment.NotMember, segment.NotMember, segment.NotMember,
		segment.Member, segment.Member, segment.Member, segment.Member,
	}

	var (
		stop, wg     = make(chan struct{}), sync.WaitGroup{}
		reads, mixed atomic.Int64
		slowest      atomic.Int64
		pings        atomic.Int64
		failed       firstError
	)

	for range e2eReaders {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}

				got, readErr := states(ctx, back.relay, "swap", probe...)
				if readErr != nil {
					failed.set(readErr)

					return
				}

				reads.Add(1)

				if !assert.ObjectsAreEqual(allOld, got) && !assert.ObjectsAreEqual(allNew, got) {
					mixed.Add(1)
				}
			}
		})
	}

	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}

			begin := time.Now()

			pingErr := back.store.Ping(ctx)
			if pingErr != nil {
				failed.set(pingErr)

				return
			}

			pings.Add(1)

			if took := time.Since(begin).Nanoseconds(); took > slowest.Load() {
				slowest.Store(took)
			}
		}
	})

	rep, err := segmentadmin.Import(ctx, back.store, target, newReader(t, &idSource{prefix: prefixNew, n: e2eIDs}),
		segmentadmin.ImportOptions{})

	close(stop)
	wg.Wait()

	require.NoError(t, err)
	require.NoError(t, failed.get(), "a reader or the pinger failed")
	assert.Equal(t, int64(e2eIDs), rep.Added)
	assert.Positive(t, reads.Load(), "the readers ran")
	assert.Positive(t, pings.Load(), "the pinger ran")
	assert.Zero(t, mixed.Load(), "a read saw a part of a set")
	assert.Less(t, time.Duration(slowest.Load()), maxStall, "a command waited for the import")

	got, err := states(ctx, back.relay, "swap", probe...)
	require.NoError(t, err)
	assert.Equal(t, allNew, got)

	stat, err := segmentadmin.Stat(ctx, back.store, target, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(e2eIDs), stat.Count)
	assert.Zero(t, stat.TTL, "the live set does not inherit the expiry of the temporary key")
}

func e2eInterrupted(t *testing.T, back *backend) {
	t.Helper()

	ctx := t.Context()
	target := e2eTarget(segCut, redissegment.LayoutSet)

	_, err := segmentadmin.Import(ctx, back.store, target, newReader(t, &idSource{prefix: prefixOld, n: 1000}),
		segmentadmin.ImportOptions{})
	require.NoError(t, err)

	cutCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	src := &idSource{prefix: prefixNew, n: e2eIDs, onLine: func(line int) {
		if line == e2eIDs/2 {
			cancel()
		}
	}}

	rep, err := segmentadmin.Import(cutCtx, back.store, target, newReader(t, src), segmentadmin.ImportOptions{})
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, rep.Swapped)

	stat, err := segmentadmin.Stat(ctx, back.store, target, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), stat.Count, "the live set is untouched")

	got, err := states(ctx, back.relay, "cut", "old-0", "old-999", "new-0")
	require.NoError(t, err)
	assert.Equal(t, []segment.State{segment.Member, segment.Member, segment.NotMember}, got)

	exists, _, err := back.store.KeyTTL(ctx, segCut+":import:new")
	require.NoError(t, err)
	assert.False(t, exists, "the temporary key is removed")
}

func e2eEdits(t *testing.T, back *backend) {
	t.Helper()

	ctx := t.Context()
	set := e2eTarget(segEdit, redissegment.LayoutSet)
	keys := e2eTarget(segFlags, redissegment.LayoutKeys)

	for _, target := range []*segmentadmin.Target{set, keys} {
		rep, err := segmentadmin.Edit(ctx, back.store, target, newReader(t, stringsReader("Ann\nBen\nann\n")), false, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(2), rep.Added)
		assert.NotEmpty(t, rep.Version)
	}

	name := map[*segmentadmin.Target]string{set: "edit", keys: "flags"}

	for _, target := range []*segmentadmin.Target{set, keys} {
		got, err := states(ctx, back.provider, name[target], "ann", "BEN", "cat")
		require.NoError(t, err)
		assert.Equal(t, []segment.State{segment.Member, segment.Member, segment.NotMember}, got)

		stat, err := segmentadmin.Stat(ctx, back.store, target, 100)
		require.NoError(t, err)
		assert.Equal(t, int64(2), stat.Count)

		rep, err := segmentadmin.Edit(ctx, back.store, target, newReader(t, stringsReader("ann\nzed\n")), true, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(1), rep.Removed)

		got, err = states(ctx, back.relay, name[target], "ann", "ben")
		require.NoError(t, err)
		assert.Equal(t, []segment.State{segment.NotMember, segment.Member}, got)

		stat, err = segmentadmin.Stat(ctx, back.store, target, 1)
		require.NoError(t, err)
		assert.Equal(t, int64(1), stat.Count)
	}
}
