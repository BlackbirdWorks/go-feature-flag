//go:build docker

package segmentadmin_test

import (
	"context"
	"os"
	"strings"
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
	return map[string]string{
		"rows":  segRows,
		"enc":   segEnc,
		"swap":  segSwap,
		"cut":   segCut,
		"edit":  segEdit,
		"flags": segFlags,
	}
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
			back := maker.build(t, srv, e2eSegments(), nil)

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
			t.Run("the lock and the conditional swap", func(t *testing.T) {
				t.Parallel()
				e2eProtocol(t, back)
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

const (
	segRows   = "semaphore:segment:{rows}"
	rowsLock  = segRows + ":import:lock"
	rowsToken = "e2e"
	rowsTmp   = segRows + ":import:" + rowsToken + ":new"
	rowsOld   = segRows + ":import:" + rowsToken + ":old"
	rowsLast  = segRows + ":import:last"
	rowsTheir = "theirs"
)

// e2eProtocol runs the rows of the protocol on the real store: the live set is never touched unless the swap condition
// holds, and a run never removes the keys or the lock of another run.
func e2eProtocol(t *testing.T, back *backend) {
	t.Helper()

	ctx := t.Context()
	target := e2eTarget(segRows, redissegment.LayoutSet)

	seed := func(t *testing.T) {
		t.Helper()

		_, _, err := back.store.Unlink(ctx, segRows, rowsLock, rowsLast, rowsTmp, rowsOld)
		require.NoError(t, err)

		_, err = back.store.SAdd(ctx, segRows, []string{"x", "y"}, 0)
		require.NoError(t, err)
	}

	liveIntact := func(t *testing.T) {
		t.Helper()

		card, err := back.store.SCard(ctx, segRows)
		require.NoError(t, err)
		assert.Equal(t, int64(2), card, "the live set is untouched")

		exists, _, err := back.store.KeyTTL(ctx, rowsOld)
		require.NoError(t, err)
		assert.False(t, exists, "the live set was never moved away")
	}

	run := func(onLine func(line int), size int, opts segmentadmin.ImportOptions) error {
		opts.Token = rowsToken
		opts.BatchSize = 1
		_, err := segmentadmin.Import(
			ctx,
			back.store,
			target,
			newReader(t, &idSource{prefix: "row", n: size, onLine: onLine}),
			opts,
		)

		return err
	}

	t.Run("a temporary key that expires between batches is not swapped in", func(t *testing.T) {
		seed(t)

		err := run(func(line int) {
			if line == 3 {
				_, _, _ = back.store.Unlink(ctx, rowsTmp)
			}
		}, 5, segmentadmin.ImportOptions{})
		require.ErrorIs(t, err, segmentadmin.ErrIncomplete)
		liveIntact(t)
	})

	t.Run("a temporary key deleted before the swap is not swapped in", func(t *testing.T) {
		seed(t)

		err := run(func(line int) {
			if line == 6 {
				_, _, _ = back.store.Unlink(ctx, rowsTmp)
			}
		}, 5, segmentadmin.ImportOptions{})
		require.ErrorIs(t, err, segmentadmin.ErrTempGone)
		liveIntact(t)
	})

	t.Run("a run that lost its lock does not swap and leaves the other run's keys", func(t *testing.T) {
		seed(t)

		_, err := back.store.SAdd(ctx, segRows+":import:"+rowsTheir+":new", []string{"theirs"}, time.Hour)
		require.NoError(t, err)

		err = run(func(line int) {
			if line == 3 {
				_ = back.store.SetValue(ctx, rowsLock, rowsTheir)
			}
		}, 5, segmentadmin.ImportOptions{})
		require.ErrorIs(t, err, segmentadmin.ErrLockLost)
		liveIntact(t)

		holder, _, err := back.store.Value(ctx, rowsLock)
		require.NoError(t, err)
		assert.Equal(t, rowsTheir, holder, "the lock of the other run is not released")

		card, err := back.store.SCard(ctx, segRows+":import:"+rowsTheir+":new")
		require.NoError(t, err)
		assert.Equal(t, int64(1), card, "the temporary key of the other run is not removed")
	})

	t.Run("a lock taken over just before the swap is not swapped over", func(t *testing.T) {
		seed(t)

		err := run(func(line int) {
			if line == 6 {
				_ = back.store.SetValue(ctx, rowsLock, rowsTheir)
			}
		}, 5, segmentadmin.ImportOptions{})
		require.ErrorIs(t, err, segmentadmin.ErrLockLost)
		liveIntact(t)
	})

	t.Run("two imports at once: one wins, the other fails at once", func(t *testing.T) {
		seed(t)

		started, release := make(chan struct{}), make(chan struct{})
		winner := make(chan error, 1)

		go func() {
			winner <- run(func(line int) {
				if line == 1 {
					close(started)
					<-release
				}
			}, 3, segmentadmin.ImportOptions{})
		}()

		<-started

		_, err := segmentadmin.Import(ctx, back.store, target, newReader(t, stringsReader("loser\n")),
			segmentadmin.ImportOptions{Token: "second"})
		require.ErrorIs(t, err, segmentadmin.ErrImportRunning)
		close(release)
		require.NoError(t, <-winner)

		got, err := states(ctx, back.relay, "rows", "row-0", "row-1", "row-2", "x", "loser")
		require.NoError(t, err)
		assert.Equal(t, []segment.State{
			segment.Member, segment.Member, segment.Member, segment.NotMember, segment.NotMember,
		}, got, "the set is exactly the input of the winner")
	})

	t.Run("the temporary key that a dead run recorded is removed by the next run", func(t *testing.T) {
		seed(t)

		dead := segRows + ":import:dead:new"
		_, err := back.store.SAdd(ctx, dead, []string{"junk"}, time.Hour)
		require.NoError(t, err)
		require.NoError(t, back.store.SetValue(ctx, rowsLast, dead))

		require.NoError(t, run(nil, 2, segmentadmin.ImportOptions{}))

		exists, _, err := back.store.KeyTTL(ctx, dead)
		require.NoError(t, err)
		assert.False(t, exists)
	})
}

// TestE2EWriterACL proves that the writer line of the documentation is enough: a user made from that very line does
// everything the tool does, on every store.
func TestE2EWriterACL(t *testing.T) {
	t.Parallel()

	page, err := os.ReadFile("../../../../docs/tooling/segment.md")
	require.NoError(t, err)

	var line string

	for text := range strings.SplitSeq(string(page), "\n") {
		if strings.HasPrefix(text, "ACL SETUSER segwriter ") {
			line = text
		}
	}

	require.NotEmpty(t, line, "the documentation has a writer line")

	args := strings.Fields(strings.Replace(line, ">secret", ">writerpass", 1))

	for _, maker := range makers() {
		t.Run(maker.name, func(t *testing.T) {
			t.Parallel()

			srv := maker.start(t)
			admin := maker.build(t, srv, e2eSegments(), nil)
			require.NoError(t, admin.admin(t.Context(), args...))

			back := maker.build(t, srv, e2eSegments(), &credentials{user: "segwriter", pass: "writerpass"})
			ctx := t.Context()

			set := e2eTarget(segEdit, redissegment.LayoutSet)
			keys := e2eTarget(segFlags, redissegment.LayoutKeys)

			_, importErr := segmentadmin.Import(ctx, back.store, set, newReader(t, &idSource{prefix: "w", n: 3000}),
				segmentadmin.ImportOptions{})
			require.NoError(t, importErr)

			_, importErr = segmentadmin.Import(ctx, back.store, set, newReader(t, &idSource{prefix: "v", n: 10}),
				segmentadmin.ImportOptions{})
			require.NoError(t, importErr)

			for _, target := range []*segmentadmin.Target{set, keys} {
				_, err = segmentadmin.Edit(ctx, back.store, target, newReader(t, stringsReader("one\ntwo\n")), false, 0)
				require.NoError(t, err)
				_, err = segmentadmin.Edit(ctx, back.store, target, newReader(t, stringsReader("one\n")), true, 0)
				require.NoError(t, err)

				stat, statErr := segmentadmin.Stat(ctx, back.store, target, 100)
				require.NoError(t, statErr)
				assert.True(t, stat.Exists)
			}

			require.NoError(t, back.store.Ping(ctx))
		})
	}
}
