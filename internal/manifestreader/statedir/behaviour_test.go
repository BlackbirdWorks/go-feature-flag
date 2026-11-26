package statedir_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

func TestStore_CostIsProportionalToTheChange(t *testing.T) {
	t.Parallel()

	const count = 2000

	contents := make([]string, count)
	for i := range contents {
		contents[i] = fmt.Sprintf(`{"flag-%d":{}}`, i)
	}

	fault := &faults{}
	run := openStore(t, &memFS{disk: newMemDisk(), faults: fault}, statedirConfig())

	run.saveAndFlush(persistOf(etagOne, manifestOne, contents...))

	// the first save writes every object once, the manifest and the layout version
	assert.Equal(t, count+2, fault.count(opCreate))
	assert.Equal(t, count+2, fault.count("write"))

	// one object changes: one object file and the manifest are written, nothing else
	changed := append([]string(nil), contents...)
	changed[count/2] = `{"flag-changed":{}}`

	fault.reset()
	run.saveAndFlush(persistOf(`"e2"`, "manifest-2", changed...))

	assert.Equal(t, 2, fault.count(opCreate), "one object and the manifest")
	assert.Equal(t, 2, fault.count("write"))
	assert.Equal(t, 2, fault.count("rename"))
	assert.Equal(t, 1, fault.count("remove objects/"+sumOf(contents[count/2])), "the old object is collected")

	// an unchanged snapshot costs no write
	fault.reset()
	run.saveAndFlush(persistOf(`"e2"`, "manifest-2", changed...))
	assert.Zero(t, fault.count(opCreate))

	// a new ETag with the same objects rewrites the manifest and no object
	fault.reset()
	run.saveAndFlush(persistOf(`"e3"`, "manifest-2", changed...))
	assert.Equal(t, 1, fault.count(opCreate))
	assert.Equal(t, 1, fault.count("rename manifest.json"))
}

func TestStore_GarbageCollection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		second     manifestreader.Persist
		wantObject []string
	}{
		{
			name:       "an object that left the manifest is removed",
			second:     persistOf(`"e2"`, "manifest-2", contentBeta, "delta"),
			wantObject: []string{contentBeta, "delta"},
		},
		{
			name:       "stale content is kept while a name still carries it",
			second:     withStale(persistOf(`"e2"`, "manifest-2", contentBeta), contentAlpha),
			wantObject: []string{contentAlpha, contentBeta},
		},
		{
			name:       "nothing left to keep",
			second:     persistOf(`"e2"`, "manifest-2"),
			wantObject: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disk := newMemDisk()
			run := openStore(t, &memFS{disk: disk, faults: &faults{}}, statedirConfig())

			run.saveAndFlush(persistOf(etagOne, manifestOne, contentAlpha, contentBeta, "gamma"))

			// leftovers of a crashed process, and a file that is not ours
			disk.write(".tmp-9", []byte("half"))
			disk.write("objects/.tmp-9", []byte("half"))
			disk.write("objects/README", []byte("not ours"))

			run.saveAndFlush(tt.second)
			run.close()

			want := []string{versionName, manifestName, "objects/README"}
			for _, content := range tt.wantObject {
				want = append(want, objectFile(content))
			}

			assert.ElementsMatch(t, want, disk.names())
		})
	}
}

func TestStore_ByteBudget(t *testing.T) {
	t.Parallel()

	const budget = 8

	tests := []struct {
		persist     manifestreader.Persist
		name        string
		wantObjects []string
		wantPartial bool
	}{
		{
			name: "current objects that fit are all saved", persist: persistOf(etagOne, "m", contentAAAA, "bbbb"),
			wantObjects: []string{contentAAAA, "bbbb"},
		},
		{
			name:        "over the budget the current objects are kept smallest first: two names come back, not one",
			persist:     persistOf(etagOne, "m", contentAAAA, "bb", "cccccc"),
			wantObjects: []string{contentAAAA, "bb"}, wantPartial: true,
		},
		{
			name:        "over the budget the stale content is dropped first",
			persist:     withStale(persistOf(etagOne, "m", contentAAAA, "bbbb", "cccc"), "dd"),
			wantObjects: []string{contentAAAA, "bbbb"}, wantPartial: true,
		},
		{
			name:        "stale content takes what is left, and no more, and that is not a partial save",
			persist:     withStale(persistOf(etagOne, "m", contentAAAA), "bbbb", "cccc"),
			wantObjects: []string{contentAAAA, sumOrderedFirst("bbbb", "cccc")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fsys := &memFS{disk: newMemDisk(), faults: &faults{}}
			run := openStore(t, fsys, statedir.Config{MaxBytes: budget})
			run.saveAndFlush(tt.persist)

			status := run.store.Hooks().StateStatus()
			assert.Empty(t, status.LastSaveError, "a partial save is not an error")
			assert.Equal(t, tt.wantPartial, status.LastSavePartial)
			assert.Equal(t, uint64(1), status.Saves)
			assert.Zero(t, status.SaveErrors)

			wantPartials := uint64(0)
			if tt.wantPartial {
				wantPartials = 1
			}

			assert.Equal(t, wantPartials, status.PartialSaves)
			run.close()

			restored, _, err := restoreOf(t, fsys)
			require.NoError(t, err)
			assertRestored(t, wantRestored{etag: etagOne, manifest: "m", objects: tt.wantObjects}, restored)
		})
	}
}

// sumOrderedFirst is the one of the contents whose checksum sorts first: the stale object that is kept when only one
// fits.
func sumOrderedFirst(contents ...string) string {
	return slices.MinFunc(contents, func(a, b string) int { return cmp.Compare(sumOf(a), sumOf(b)) })
}

func TestStore_SaveNeverWaitsForTheDisk(t *testing.T) {
	t.Parallel()

	var (
		once    sync.Once
		blocked = make(chan struct{})
		release = make(chan struct{})
	)

	fault := &faults{gate: func(op string) {
		if op != opCreate {
			return
		}

		once.Do(func() {
			close(blocked)

			<-release
		})
	}}

	disk := newMemDisk()
	run := openStore(t, &memFS{disk: disk, faults: fault}, statedirConfig())
	hooks := run.store.Hooks()

	hooks.Save(persistOf(etagOne, manifestOne, "one"))

	select {
	case <-blocked:
	case <-time.After(wait):
		require.FailNow(t, "the writer did not start")
	}

	// the disk is stuck: Save returns at once and the snapshots queue latest-wins
	returned := make(chan struct{})

	go func() {
		hooks.Save(persistOf(`"e2"`, "manifest-2", "two"))
		hooks.Save(persistOf(`"e3"`, "manifest-3", "three"))
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(wait):
		require.FailNow(t, "Save waited for the disk")
	}

	assert.True(t, hooks.StateStatus().Pending)

	close(release)
	run.flush()

	assert.False(t, hooks.StateStatus().Pending)
	assert.Equal(
		t,
		2,
		fault.count("rename manifest.json"),
		"the snapshot in flight and the latest, never the one between",
	)

	run.close()

	restored, _, err := restoreOf(t, &memFS{disk: disk, faults: &faults{}})
	require.NoError(t, err)
	assertRestored(t, wantRestored{etag: `"e3"`, manifest: "manifest-3", objects: []string{"three"}}, restored)
}

func TestStore_FlushIsBoundedByItsContext(t *testing.T) {
	t.Parallel()

	// no writer is running: a queued snapshot is never written, and Flush gives up with its context
	store, err := statedir.New(statedir.Config{FS: &memFS{disk: newMemDisk(), faults: &faults{}}, Dir: stateDirName})
	require.NoError(t, err)

	store.Hooks().Save(persistOf(etagOne, manifestOne))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, store.Flush(ctx), context.Canceled)
	require.NoError(t, store.Close())
}

func TestStore_SavingANilSnapshotDoesNothing(t *testing.T) {
	t.Parallel()

	fault := &faults{}
	run := openStore(t, &memFS{disk: newMemDisk(), faults: fault}, statedirConfig())

	run.store.Hooks().Save(manifestreader.Persist{ETag: etagOne})
	run.flush()

	assert.Zero(t, fault.steps)
	assert.Equal(t, manifestreader.StateStatus{Enabled: true}, run.store.Hooks().StateStatus())
}

func TestStore_TwoWritersOnOneDirectory(t *testing.T) {
	t.Parallel()

	t.Run("the lock refuses the second owner", func(t *testing.T) {
		t.Parallel()

		fsys := &memFS{disk: newMemDisk(), faults: &faults{}}
		first, err := statedir.New(statedir.Config{FS: fsys, Dir: stateDirName})
		require.NoError(t, err)

		_, err = statedir.New(statedir.Config{FS: fsys, Dir: stateDirName})
		require.ErrorIs(t, err, statedir.ErrLocked)

		require.NoError(t, first.Close())

		second, err := statedir.New(statedir.Config{FS: fsys, Dir: stateDirName})
		require.NoError(t, err)
		require.NoError(t, second.Close())
	})

	t.Run("without a lock the last writer wins and nothing is corrupted", func(t *testing.T) {
		t.Parallel()

		const rounds = 30

		disk := newMemDisk()
		fsys := &memFS{disk: disk, faults: &faults{}, noLock: true}
		left := openStore(t, fsys, statedirConfig())
		right := openStore(t, fsys, statedirConfig())

		states := map[string]wantRestored{}

		var wg sync.WaitGroup

		for i := range rounds {
			// the two writers save different states at the same time
			leftState := persistOf(
				fmt.Sprintf(`"l%d"`, i),
				fmt.Sprintf("manifest-l%d", i),
				fmt.Sprintf("l-%d", i),
				contentShared,
			)
			rightState := persistOf(
				fmt.Sprintf(`"r%d"`, i),
				fmt.Sprintf("manifest-r%d", i),
				fmt.Sprintf("r-%d", i),
				contentShared,
			)
			states[fmt.Sprintf(`"l%d"`, i)] = wantRestored{
				etag: fmt.Sprintf(`"l%d"`, i), manifest: fmt.Sprintf("manifest-l%d", i),
				objects: []string{fmt.Sprintf("l-%d", i), contentShared},
			}
			states[fmt.Sprintf(`"r%d"`, i)] = wantRestored{
				etag: fmt.Sprintf(`"r%d"`, i), manifest: fmt.Sprintf("manifest-r%d", i),
				objects: []string{fmt.Sprintf("r-%d", i), contentShared},
			}

			wg.Go(func() { left.saveAndFlushAsserting(leftState) })
			wg.Go(func() { right.saveAndFlushAsserting(rightState) })
			wg.Wait()

			restored, status, err := restoreOf(t, &memFS{disk: disk, faults: &faults{}, noLock: true})
			require.NoError(t, err)

			// the manifest and its ETag belong together, whoever wrote them; every object returned verified,
			// belongs to that state, and an object that the other writer collected is simply absent
			want, known := states[restored.ETag]
			require.True(t, known, "an ETag that nobody saved: %s", restored.ETag)
			assert.Equal(t, want.manifest, string(restored.Manifest))

			for _, content := range restored.Objects {
				assert.Contains(t, want.objects, string(content))
			}

			assert.Equal(t, len(want.objects)-len(restored.Objects), status.DiscardedObjects)
		}
	})
}

func TestNew_Refuses(t *testing.T) {
	t.Parallel()

	_, err := statedir.New(statedir.Config{})
	require.ErrorIs(t, err, statedir.ErrNoDir)

	_, err = statedir.New(statedir.Config{FS: failingFS{err: errNotAvailable}, Dir: stateDirName})
	require.ErrorIs(t, err, errNotAvailable)
}

var errNotAvailable = errors.New("the directory is not available")

type failingFS struct{ err error }

func (f failingFS) Open(string) (statedir.Root, error) { //nolint:ireturn // implements the FS seam
	return nil, f.err
}
