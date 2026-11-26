package statedir_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

const (
	stateDirName  = "state"
	versionName   = "VERSION"
	manifestName  = "manifest.json"
	manifestOne   = "manifest-1"
	opCreate      = "create"
	etagOne       = `"e1"`
	contentAlpha  = "alpha"
	contentBeta   = "beta"
	contentShared = "shared"
	contentAAAA   = "aaaa"
)

// wait bounds every wait of a test: a missing event fails the test instead of hanging it.
const wait = 10 * time.Second

// persistOf is what the reader hands to Save: an accepted manifest with its ETag and the ok objects.
func persistOf(etag, manifest string, ok ...string) manifestreader.Persist {
	objects := map[string]manifestreader.ObjectState{}

	for i, content := range ok {
		objects[fmt.Sprintf("ok-%d", i)] = stateOf(manifestreader.StateOK, content)
	}

	return manifestreader.Persist{
		Snapshot: &manifestreader.Snapshot{Generation: 1, Channel: "stable", Objects: objects},
		ETag:     etag, Manifest: []byte(manifest),
	}
}

func stateOf(state manifestreader.State, content string) manifestreader.ObjectState {
	return manifestreader.ObjectState{
		Kind: "flags", Format: "json", Checksum: controlplane.ChecksumOf([]byte(content)), State: state,
		Bytes: []byte(content),
	}
}

// withStale adds stale objects (the earlier content a failed name still carries).
func withStale(p manifestreader.Persist, stale ...string) manifestreader.Persist {
	for i, content := range stale {
		p.Snapshot.Objects[fmt.Sprintf("stale-%d", i)] = stateOf(manifestreader.StateStale, content)
	}

	return p
}

// withMissing adds an object that never loaded: no bytes, nothing to save.
func withMissing(p manifestreader.Persist) manifestreader.Persist {
	p.Snapshot.Objects["missing"] = manifestreader.ObjectState{State: manifestreader.StateMissing}

	return p
}

func sumOf(content string) string { return controlplane.ChecksumOf([]byte(content)).Value }

func objectFile(content string) string { return "objects/" + sumOf(content) }

// running is a store with its writer running.
type running struct {
	t      testing.TB
	store  *statedir.Store
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func openStore(tb testing.TB, fsys statedir.FS, cfg statedir.Config) *running {
	tb.Helper()

	cfg.FS = fsys
	if cfg.Dir == "" {
		cfg.Dir = stateDirName
	}

	store, err := statedir.New(cfg)
	require.NoError(tb, err)

	ctx, cancel := context.WithCancel(tb.Context())
	run := &running{t: tb, store: store, cancel: cancel, done: make(chan struct{})}

	go func() {
		store.Run(ctx)
		close(run.done)
	}()

	tb.Cleanup(run.close)

	return run
}

// close stops the writer and releases the directory; it may be called more than once.
func (r *running) close() {
	r.once.Do(func() {
		r.cancel()

		select {
		case <-r.done:
			require.NoError(r.t, r.store.Close())
		case <-time.After(wait):
			assert.Fail(r.t, "Run did not return")
		}
	})
}

// saveAndFlush saves and waits until it is written.
func (r *running) saveAndFlush(p manifestreader.Persist) {
	r.t.Helper()

	r.store.Hooks().Save(p)
	r.flush()
}

func (r *running) flush() {
	r.t.Helper()

	ctx, cancel := context.WithTimeout(r.t.Context(), wait)
	defer cancel()

	require.NoError(r.t, r.store.Flush(ctx))
}

// restoreOf is what a new store on the same disk restores, and its status.
func restoreOf(
	tb testing.TB,
	fsys statedir.FS,
) (*manifestreader.Restored, manifestreader.StateStatus, error) {
	tb.Helper()

	store, err := statedir.New(statedir.Config{FS: fsys, Dir: stateDirName})
	require.NoError(tb, err)

	defer func() { require.NoError(tb, store.Close()) }()

	restored, err := store.Hooks().Restore()

	return restored, store.Hooks().StateStatus(), err
}

// wantRestored is the expected restore of a saved persist.
type wantRestored struct {
	etag     string
	manifest string
	objects  []string
}

func assertRestored(tb testing.TB, want wantRestored, got *manifestreader.Restored) {
	tb.Helper()

	require.NotNil(tb, got)
	assert.Equal(tb, want.etag, got.ETag)
	assert.Equal(tb, want.manifest, string(got.Manifest))

	contents := make([]string, 0, len(got.Objects))

	for _, content := range got.Objects {
		contents = append(contents, string(content))
	}

	assert.ElementsMatch(tb, want.objects, contents)
}

func statedirConfig() statedir.Config { return statedir.Config{} }

func fixedNow() time.Time { return clocktest.Instant() }
