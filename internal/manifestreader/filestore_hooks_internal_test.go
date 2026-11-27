package manifestreader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// These tests step into a Get at exact points of its read, through the hooks of the store: a context that ends
// during a read, a writer that changes the file between the two passes, a read that fails half way.

const (
	hookSparseSize = 8 << 20
	hookKey        = "dir/object.json"
	heldETag       = `"held"`
)

// countdownContext reports an error after a number of looks at it: the context ends "during the read", whatever
// the speed of the machine.
type countdownContext struct {
	context.Context //nolint:containedctx // it wraps a context to change its Err

	err  error
	left atomic.Int64
}

func newCountdown(parent context.Context, looks int64, err error) *countdownContext {
	ctx := &countdownContext{Context: parent, err: err}
	ctx.left.Store(looks)

	return ctx
}

func (c *countdownContext) Err() error {
	if c.left.Add(-1) < 0 {
		return c.err
	}

	return nil
}

// countingWrap counts the bytes the file served in one pass.
type countingWrap struct {
	served atomic.Int64
}

func (c *countingWrap) wrap(_ int, src io.Reader) io.Reader {
	return countReader{src: src, total: &c.served}
}

type countReader struct {
	src   io.Reader
	total *atomic.Int64
}

func (r countReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	r.total.Add(int64(n))

	return n, err
}

func sparseFile(t *testing.T, dir string, size int64) {
	t.Helper()

	file, err := os.Create(filepath.Join(dir, filepath.FromSlash(hookKey)))
	require.NoError(t, err)
	require.NoError(t, file.Truncate(size))
	require.NoError(t, file.Close())
}

func hookStore(t *testing.T, hooks *fileHooks) (*FileStore, string) {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dir"), 0o750))

	return &FileStore{root: dir, hooks: hooks}, dir
}

func etagOfBytes(data []byte) string {
	sum := sha256.Sum256(data)

	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// TestFileStore_ContextEndsDuringTheRead: the context of a read of a large file ends after a few looks. The read
// stops with the context error and has read only a few chunks of the file, in the hashing pass and in the read
// pass, for a cancellation and for a deadline.
func TestFileStore_ContextEndsDuringTheRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cause         error
		name          string
		etag          string
		wantReason    string
		looks         int64
		wantRetryable bool
	}{
		{
			name: "cancelled during the hashing pass", cause: context.Canceled, etag: heldETag, looks: 4,
			wantReason: ReasonError, wantRetryable: true,
		},
		{
			name: "deadline during the hashing pass", cause: context.DeadlineExceeded, etag: heldETag, looks: 4,
			wantReason: ReasonError, wantRetryable: true,
		},
		{
			name: "cancelled during the read pass", cause: context.Canceled, looks: 4,
			wantReason: ReasonError, wantRetryable: true,
		},
		{
			name: "deadline during the read pass", cause: context.DeadlineExceeded, looks: 4,
			wantReason: ReasonError, wantRetryable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			counter := &countingWrap{}
			store, dir := hookStore(t, &fileHooks{wrap: counter.wrap})
			sparseFile(t, dir, hookSparseSize)

			ctx := newCountdown(t.Context(), tt.looks, tt.cause)

			obj, err := store.Get(ctx, GetRequest{Key: hookKey, ETag: tt.etag, Limit: hookSparseSize})
			require.ErrorIs(t, err, tt.cause)
			assert.Equal(t, Object{}, obj)

			fetchErr, ok := errors.AsType[*FetchError](err)
			require.True(t, ok)
			assert.Equal(t, tt.wantRetryable, fetchErr.Retryable)
			assert.Equal(t, tt.wantReason, fetchErr.Reason)
			assert.LessOrEqual(t, counter.served.Load(), tt.looks*fileChunk, "the read stopped with the context")
			assert.Less(t, counter.served.Load(), int64(hookSparseSize))
		})
	}
}

// TestFileStore_FileChangesBetweenThePasses: whatever a writer does between the hashing pass and the read pass,
// the answer pairs bytes with the ETag of exactly those bytes, or is a clean error.
func TestFileStore_FileChangesBetweenThePasses(t *testing.T) {
	t.Parallel()

	original := []byte("0123456789abcdef")

	tests := []struct {
		change    func(t *testing.T, path string)
		name      string
		wantBytes []byte
	}{
		{
			name: "the file is removed: the open file is still read", wantBytes: original,
			change: func(t *testing.T, path string) {
				t.Helper()
				require.NoError(t, os.Remove(path))
			},
		},
		{
			name: "the file is replaced by a rename: the old content is read", wantBytes: original,
			change: func(t *testing.T, path string) {
				t.Helper()
				require.NoError(t, os.WriteFile(path+".new", []byte("replacement"), 0o600))
				require.NoError(t, os.Rename(path+".new", path))
			},
		},
		{
			name: "the file is rewritten in place", wantBytes: []byte("ABCDEFGHIJKLMNOP"),
			change: func(t *testing.T, path string) {
				t.Helper()
				require.NoError(t, os.WriteFile(path, []byte("ABCDEFGHIJKLMNOP"), 0o600))
			},
		},
		{
			name: "the file is cut in the middle in place (a torn read)", wantBytes: original[:8],
			change: func(t *testing.T, path string) {
				t.Helper()
				require.NoError(t, os.Truncate(path, 8))
			},
		},
		{
			name: "the file is emptied in place", wantBytes: []byte{},
			change: func(t *testing.T, path string) {
				t.Helper()
				require.NoError(t, os.Truncate(path, 0))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var path string

			store, dir := hookStore(t, &fileHooks{between: func() { tt.change(t, path) }})
			path = filepath.Join(dir, filepath.FromSlash(hookKey))
			require.NoError(t, os.WriteFile(path, original, 0o600))

			obj, err := store.Get(t.Context(), GetRequest{Key: hookKey, ETag: heldETag, Limit: 1 << 10})
			require.NoError(t, err)
			assert.Equal(t, tt.wantBytes, bytes.Clone(obj.Bytes))
			assert.Equal(t, etagOfBytes(tt.wantBytes), obj.ETag, "the ETag is the one of the bytes returned")
		})
	}
}

func TestFileStore_FileChangesBackToTheHeldContentBetweenThePasses(t *testing.T) {
	t.Parallel()

	var path string

	store, dir := hookStore(t, &fileHooks{between: func() {
		require.NoError(t, os.WriteFile(path, []byte("held content"), 0o600))
	}})
	path = filepath.Join(dir, filepath.FromSlash(hookKey))
	require.NoError(t, os.WriteFile(path, []byte("other content"), 0o600))

	obj, err := store.Get(t.Context(), GetRequest{Key: hookKey, ETag: etagOfBytes([]byte("held content")), Limit: 100})
	require.NoError(t, err, "a full answer, not a not-modified one: the hashing pass saw other bytes")
	assert.Equal(t, []byte("held content"), obj.Bytes)
	assert.Equal(t, etagOfBytes([]byte("held content")), obj.ETag)
}

func TestFileStore_ReadErrorsAndGrowth(t *testing.T) {
	t.Parallel()

	failing := func(pass int) func(int, io.Reader) io.Reader {
		return func(got int, src io.Reader) io.Reader {
			if got != pass {
				return src
			}

			return io.MultiReader(io.LimitReader(src, 3), errReader{})
		}
	}

	growing := func(got int, src io.Reader) io.Reader {
		if got != passHash {
			return src
		}

		return io.MultiReader(src, bytes.NewReader([]byte("grew after the open")))
	}

	tests := []struct {
		wrap          func(int, io.Reader) io.Reader
		wantIs        error
		name          string
		etag          string
		wantReason    string
		wantRetryable bool
	}{
		{
			name: "a read error during the hashing pass", wrap: failing(passHash), etag: heldETag,
			wantIs: errDisk, wantReason: ReasonError, wantRetryable: true,
		},
		{
			name: "a read error during the read pass", wrap: failing(passRead),
			wantIs: errDisk, wantReason: ReasonError, wantRetryable: true,
		},
		{
			name: "a file that grew past the limit after it was opened", wrap: growing, etag: heldETag,
			wantIs: ErrTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, dir := hookStore(t, &fileHooks{wrap: tt.wrap})
			require.NoError(
				t,
				os.WriteFile(filepath.Join(dir, filepath.FromSlash(hookKey)), []byte("0123456789"), 0o600),
			)

			obj, err := store.Get(t.Context(), GetRequest{Key: hookKey, ETag: tt.etag, Limit: 10})
			require.ErrorIs(t, err, tt.wantIs)
			assert.Equal(t, Object{}, obj)

			if tt.wantReason != "" {
				fetchErr, ok := errors.AsType[*FetchError](err)
				require.True(t, ok)
				assert.Equal(t, tt.wantReason, fetchErr.Reason)
				assert.Equal(t, tt.wantRetryable, fetchErr.Retryable)
			}
		})
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errDisk }

func TestFileStore_StatOfTheOpenFileFails(t *testing.T) {
	t.Parallel()

	store, dir := hookStore(t, &fileHooks{stat: func(*os.File) (os.FileInfo, error) { return nil, &fsError{errno: 5} }})
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(hookKey)), []byte("x"), 0o600))

	obj, err := store.Get(t.Context(), GetRequest{Key: hookKey, Limit: 10})
	assert.Equal(t, Object{}, obj)

	fetchErr, ok := errors.AsType[*FetchError](err)
	require.True(t, ok)
	assert.Equal(t, ReasonError, fetchErr.Reason)
	assert.True(t, fetchErr.Retryable)
}

// TestFileStore_ConcurrentNonAtomicWriter: a writer rewrites the file in place, over and over, while readers read
// it. Every answer pairs bytes with the ETag of exactly those bytes, whatever mixture of old and new bytes the
// reader saw (a torn read has a hash of its own).
func TestFileStore_ConcurrentNonAtomicWriter(t *testing.T) {
	t.Parallel()

	store, dir := hookStore(t, nil)
	path := filepath.Join(dir, filepath.FromSlash(hookKey))
	contents := [][]byte{bytes.Repeat([]byte("a"), 100_000), bytes.Repeat([]byte("b"), 90_000), []byte("short")}
	require.NoError(t, os.WriteFile(path, contents[0], 0o600))

	const writes, readers, reads = 60, 4, 30

	done := make(chan struct{})

	go func() {
		defer close(done)

		for i := range writes {
			assert.NoError(
				t,
				os.WriteFile(path, contents[i%len(contents)], 0o600),
			)
		}
	}()

	results := make(chan Object, readers*reads)

	var readersDone atomic.Int32

	for range readers {
		go func() {
			defer func() {
				if readersDone.Add(1) == readers {
					close(results)
				}
			}()

			for range reads {
				obj, err := store.Get(t.Context(), GetRequest{Key: hookKey, Limit: 1 << 20})
				if assert.NoError(t, err) {
					results <- obj
				}
			}
		}()
	}

	for obj := range results {
		assert.Equal(t, etagOfBytes(obj.Bytes), obj.ETag, "the ETag of the bytes returned, whatever they are")
	}

	<-done
}

// ---- the reader over a store that is read while a writer is at work ----

// tornHarness runs a Reader over a FileStore whose hooks tear the next read of a file in place.
type tornHarness struct {
	tearKey atomic.Value // the key whose next read is torn
	t       *testing.T
	reader  *Reader
	sched   *roundScheduler
	root    string
}

type roundScheduler struct {
	entered chan struct{}
	release chan struct{}
}

func (s *roundScheduler) Wait(ctx context.Context, _ time.Duration) error {
	s.entered <- struct{}{}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.release:
		return nil
	}
}

func (h *tornHarness) round() {
	h.t.Helper()

	h.sched.release <- struct{}{}

	<-h.sched.entered
}

func (h *tornHarness) snapshot() *Snapshot {
	select {
	case snap := <-h.reader.Snapshots():
		return snap
	default:
		return nil
	}
}

func objectEntry(t *testing.T, name, content string) controlplane.ManifestObject {
	t.Helper()

	sum := controlplane.ChecksumOf([]byte(content))
	key, err := controlplane.ObjectKey(sum, "json")
	require.NoError(t, err)

	return controlplane.ManifestObject{
		Name: name, Kind: controlplane.KindFlags, Connection: "control", Key: key, Format: controlplane.FormatJSON,
		Checksum: sum, Size: int64(len(content)),
	}
}

func writeManifest(t *testing.T, root string, gen uint64, entries ...controlplane.ManifestObject) []byte {
	t.Helper()

	data, err := controlplane.MarshalManifest(&controlplane.Manifest{
		APIVersion: controlplane.APIVersion, Kind: controlplane.DocumentKindManifest, Channel: "stable",
		Generation: gen, Objects: entries,
	})
	require.NoError(t, err)

	path := filepath.Join(root, "manifest.yaml")
	require.NoError(t, os.WriteFile(path+".tmp", data, 0o600))
	require.NoError(t, os.Rename(path+".tmp", path))

	return data
}

func writeObject(t *testing.T, root string, entry *controlplane.ManifestObject, content string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(entry.Key))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// startTorn publishes generation 1 (alpha) and starts a reader whose store tears the reads of tearKey: the file
// is cut in half, in place, at the moment the store reads it (between the passes of a conditional read, at the
// start of the read pass of an unconditional one).
func startTorn(t *testing.T) (*tornHarness, *controlplane.ManifestObject) {
	t.Helper()

	root := t.TempDir()
	harness := &tornHarness{t: t, root: root}

	tear := func() {
		key, _ := harness.tearKey.Load().(string)
		if key == "" {
			return
		}

		harness.tearKey.Store("")

		path := filepath.Join(root, filepath.FromSlash(key))
		info, err := os.Stat(path)
		assert.NoError(t, err)
		assert.NoError(t, os.Truncate(path, info.Size()/2))
	}

	store := &FileStore{root: root, hooks: &fileHooks{
		between: tear,
		wrap: func(pass int, src io.Reader) io.Reader {
			if pass != passRead {
				return src
			}

			tear() // an unconditional read has no pass in between: it is torn as it starts

			return src
		},
	}}

	alpha := objectEntry(t, "alpha", `{"alpha":1}`)
	writeObject(t, root, &alpha, `{"alpha":1}`)
	writeManifest(t, root, 1, alpha)

	harness.sched = &roundScheduler{entered: make(chan struct{}, 8), release: make(chan struct{})}

	reader, err := New(store, &Config{
		ManifestKey: "manifest.yaml", Connection: "control", Channel: "stable", DisableJitter: true,
		FetchConcurrency: 1,
	}, WithScheduler(harness.sched), WithJitter(func() float64 { return 0 }))
	require.NoError(t, err)

	harness.reader = reader

	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan struct{})

	go func() {
		defer close(finished)

		_ = reader.Run(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		<-finished
	})

	<-harness.sched.entered
	require.NotNil(t, harness.snapshot(), "generation 1 is served after the first round")

	return harness, &alpha
}

func TestReader_TornManifestReadIsRejectedAndRecovers(t *testing.T) {
	t.Parallel()

	harness, alpha := startTorn(t)

	beta := objectEntry(t, "beta", `{"beta":2}`)
	writeObject(t, harness.root, &beta, `{"beta":2}`)

	// the reader polls while the manifest of generation 2 is being written: it reads half of it
	full := writeManifest(t, harness.root, 2, *alpha, beta)
	harness.tearKey.Store("manifest.yaml")
	harness.round()

	assert.Nil(t, harness.snapshot())
	assert.Equal(t, OutcomeInvalid, harness.reader.Status().Manifest.Outcome)
	assert.Equal(t, uint64(1), harness.reader.Status().Manifest.Generation, "generation 1 is still held")

	// the writer is done: the next poll reads the whole file, which has another ETag
	require.NoError(t, os.WriteFile(filepath.Join(harness.root, "manifest.yaml"), full, 0o600))
	harness.round()

	snap := harness.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, uint64(2), snap.Generation)
	assert.Equal(t, StateOK, snap.Objects["beta"].State)
	assert.Equal(t, OutcomeAccepted, harness.reader.Status().Manifest.Outcome)
}

func TestReader_TornObjectReadIsStaleAndRecovers(t *testing.T) {
	t.Parallel()

	harness, alpha := startTorn(t)

	alpha2 := objectEntry(t, "alpha", `{"alpha":"second"}`)
	writeObject(t, harness.root, &alpha2, `{"alpha":"second"}`)
	writeManifest(t, harness.root, 2, alpha2)

	// the object file is cut in half in place as the reader reads it
	harness.tearKey.Store(alpha2.Key)
	harness.round()

	snap := harness.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, StateStale, snap.Objects["alpha"].State)
	assert.Equal(t, ReasonSizeMismatch, snap.Objects["alpha"].Reason)
	assert.Equal(t, []byte(`{"alpha":1}`), snap.Objects["alpha"].Bytes, "the last good content is served")
	assert.Equal(t, alpha.Checksum, snap.Objects["alpha"].Checksum)

	// the writer finishes: the next poll reads the whole file
	writeObject(t, harness.root, &alpha2, `{"alpha":"second"}`)
	harness.round()

	snap = harness.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, StateOK, snap.Objects["alpha"].State)
	assert.JSONEq(t, `{"alpha":"second"}`, string(snap.Objects["alpha"].Bytes))
}
