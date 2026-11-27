package manifestreader_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// recordingStore is the call log of the reader: what it asked the store, in the order it asked.
type recordingStore struct {
	inner manifestreader.ObjectStore
	calls []call
	mu    sync.Mutex
}

func (r *recordingStore) Get(ctx context.Context, req manifestreader.GetRequest) (manifestreader.Object, error) {
	r.mu.Lock()
	r.calls = append(r.calls, call{Key: req.Key, ETag: req.ETag, VersionID: req.VersionID})
	r.mu.Unlock()

	return r.inner.Get(ctx, req)
}

func (r *recordingStore) take() []call {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := r.calls
	r.calls = nil

	return out
}

// fileRig is a reader on a FileStore over a real directory.
type fileRig struct {
	*rig

	rec  *recordingStore
	root string
}

// startFileRig starts the reader over root and waits for its first round. The calls of that round are kept.
func startFileRig(t *testing.T, root string, opts ...manifestreader.Option) *fileRig {
	t.Helper()

	rec := &recordingStore{inner: newFileStoreAt(t, root)}
	rdr := launchRig(t, rec, &manifestreader.Config{DisableJitter: true}, opts...)
	rdr.settle()

	return &fileRig{rig: rdr, rec: rec, root: root}
}

// roundCalls ticks one round and returns the exact requests of the reader, in the order it made them (the
// manifest first, then the objects by key).
func (f *fileRig) roundCalls() []call {
	f.t.Helper()

	f.rec.take()
	f.round()

	return sortCalls(f.rec.take())
}

func atomicWrite(t *testing.T, path string, content []byte) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))

	tmp := path + ".tmp"
	require.NoError(t, os.WriteFile(tmp, content, 0o600))
	require.NoError(t, os.Rename(tmp, path))
}

// publishFiles writes the objects, then the manifest, each atomically: the order of the publisher.
func publishFiles(t *testing.T, root string, gen uint64, objs ...obj) {
	t.Helper()

	for _, o := range objs {
		atomicWrite(t, filepath.Join(root, filepath.FromSlash(keyOf(t, o))), []byte(o.content))
	}

	atomicWrite(t, filepath.Join(root, filepath.FromSlash(manifestKey)), mustMarshal(t, manifestOf(t, gen, objs...)))
}

// manifestETagOnDisk is the ETag the reader holds after it read the manifest file as it is now.
func manifestETagOnDisk(t *testing.T, root string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(manifestKey)))
	require.NoError(t, err)

	return etagOf(data)
}

func allOK(t *testing.T, objs ...obj) map[string]manifestreader.ObjectState {
	t.Helper()

	out := map[string]manifestreader.ObjectState{}
	for _, o := range objs {
		out[o.name] = okState(t, o)
	}

	return out
}

func TestFileReader_ColdStartThenSteadyState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	objs := threeObjects()
	publishFiles(t, root, 1, objs...)

	rdr := startFileRig(t, root)

	assert.Equal(t, coldCalls(t, objs...), sortCalls(rdr.rec.take()), "the manifest, then each object once")
	assert.Equal(t, snapOf(1, allOK(t, objs...)), rdr.snapshot())

	etag := manifestETagOnDisk(t, root)

	const polls = 5

	for i := range polls {
		assert.Equal(
			t,
			[]call{manifestCall(etag)},
			rdr.roundCalls(),
			"poll %d: one conditional read and nothing else",
			i,
		)
		assert.Nil(t, rdr.snapshot())
	}

	assert.Equal(t, uint64(polls), rdr.reader.Status().NotModified)
}

func TestFileReader_OneObjectChanges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	objs := threeObjects()
	publishFiles(t, root, 1, objs...)

	rdr := startFileRig(t, root)
	require.NotNil(t, rdr.snapshot())

	etag1 := manifestETagOnDisk(t, root)
	alpha2 := obj{name: nameAlpha, content: contentAlphaChanged}
	publishFiles(t, root, 2, alpha2, objs[1], objs[2])

	assert.Equal(t, []call{manifestCall(etag1), objCall(t, alpha2)}, rdr.roundCalls())
	assert.Equal(t, snapOf(2, allOK(t, alpha2, objs[1], objs[2])), rdr.snapshot())

	assert.Equal(t, []call{manifestCall(manifestETagOnDisk(t, root))}, rdr.roundCalls(), "quiet again")
	assert.Nil(t, rdr.snapshot())
}

func TestFileReader_NewGenerationWithTheSameObjectsFetchesNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	objs := threeObjects()
	publishFiles(t, root, 1, objs...)

	rdr := startFileRig(t, root)
	require.NotNil(t, rdr.snapshot())

	etag1 := manifestETagOnDisk(t, root)
	atomicWrite(t, filepath.Join(root, filepath.FromSlash(manifestKey)), mustMarshal(t, manifestOf(t, 2, objs...)))

	assert.Equal(t, []call{manifestCall(etag1)}, rdr.roundCalls(), "the manifest is read, no object is")
	assert.Equal(t, snapOf(2, allOK(t, objs...)), rdr.snapshot())
}

// configMapMount publishes into a directory laid out as kubelet lays out a ConfigMap volume: the files live in a
// timestamped directory, ..data is a link to it, and each top-level name is a link through ..data. A new
// version is a new directory, a new ..data_tmp link renamed over ..data, and the old directory removed.
type configMapMount struct {
	root    string
	current string
}

func (m *configMapMount) publish(t *testing.T, version string, gen uint64, objs ...obj) {
	t.Helper()

	dir := filepath.Join(m.root, "..v"+version)

	for _, o := range objs {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, filepath.Dir(filepath.FromSlash(keyOf(t, o)))), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(keyOf(t, o))), []byte(o.content), 0o600))
	}

	manifestPath := filepath.Join(dir, filepath.FromSlash(manifestKey))
	require.NoError(t, os.MkdirAll(filepath.Dir(manifestPath), 0o750))
	require.NoError(t, os.WriteFile(manifestPath, mustMarshal(t, manifestOf(t, gen, objs...)), 0o600))

	symlinkOrSkip(t, "..v"+version, filepath.Join(m.root, "..data_tmp"))
	require.NoError(t, os.Rename(filepath.Join(m.root, "..data_tmp"), filepath.Join(m.root, "..data")))

	if m.current == "" {
		symlinkOrSkip(t, "..data/tenant", filepath.Join(m.root, "tenant"))
	} else {
		require.NoError(t, os.RemoveAll(filepath.Join(m.root, "..v"+m.current)))
	}

	m.current = version
}

func TestFileReader_AtomicDirectorySwapOfAConfigMapMount(t *testing.T) {
	t.Parallel()

	mount := &configMapMount{root: t.TempDir()}
	objs := threeObjects()
	mount.publish(t, "1", 1, objs...)

	rdr := startFileRig(t, mount.root)
	assert.Equal(t, coldCalls(t, objs...), sortCalls(rdr.rec.take()))
	assert.Equal(t, snapOf(1, allOK(t, objs...)), rdr.snapshot())

	etag1 := manifestETagOnDisk(t, mount.root)
	assert.Equal(t, []call{manifestCall(etag1)}, rdr.roundCalls(), "a mount nobody touched costs one read")

	alpha2 := obj{name: nameAlpha, content: contentAlphaChanged}
	mount.publish(t, "2", 2, alpha2, objs[1], objs[2])

	assert.Equal(
		t,
		[]call{manifestCall(etag1), objCall(t, alpha2)},
		rdr.roundCalls(),
		"the swap is seen by the manifest",
	)
	assert.Equal(t, snapOf(2, allOK(t, alpha2, objs[1], objs[2])), rdr.snapshot())

	assert.Equal(t, []call{manifestCall(manifestETagOnDisk(t, mount.root))}, rdr.roundCalls())

	// a swap that changes nothing in the manifest is a quiet poll, whatever directory the files come from
	mount.publish(t, "3", 2, alpha2, objs[1], objs[2])
	assert.Equal(t, []call{manifestCall(manifestETagOnDisk(t, mount.root))}, rdr.roundCalls())
	assert.Nil(t, rdr.snapshot())
}

func TestFileReader_PartiallyWrittenObjectIsStaleThenRecovers(t *testing.T) {
	t.Parallel()

	alpha2 := obj{name: nameAlpha, content: contentAlphaSecond}

	tests := []struct {
		name       string
		partial    string
		wantReason string
	}{
		{
			name:       "the writer stopped half way",
			partial:    contentAlphaSecond[:6],
			wantReason: manifestreader.ReasonSizeMismatch,
		},
		{
			name:       "the writer wrote all the bytes, wrong ones",
			partial:    `{"alpha":"SECOND"}`,
			wantReason: manifestreader.ReasonChecksumMismatch,
		},
		{name: "the writer has not started: an empty file", partial: "", wantReason: manifestreader.ReasonSizeMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			objs := threeObjects()
			publishFiles(t, root, 1, objs...)

			rdr := startFileRig(t, root)
			require.NotNil(t, rdr.snapshot())

			etag1 := manifestETagOnDisk(t, root)

			// the manifest is in place, the object is being written (not atomically) while the reader polls
			objPath := filepath.Join(root, filepath.FromSlash(keyOf(t, alpha2)))
			atomicWrite(
				t,
				filepath.Join(root, filepath.FromSlash(manifestKey)),
				mustMarshal(t, manifestOf(t, 2, alpha2, objs[1], objs[2])),
			)
			require.NoError(t, os.MkdirAll(filepath.Dir(objPath), 0o750))
			require.NoError(t, os.WriteFile(objPath, []byte(tt.partial), 0o600))

			assert.Equal(t, []call{manifestCall(etag1), objCall(t, alpha2)}, rdr.roundCalls())

			stale := staleAlpha(t, tt.wantReason)
			assert.Equal(t, snapOf(2, map[string]manifestreader.ObjectState{
				nameAlpha: stale, nameBeta: okState(t, objs[1]), nameGamma: okState(t, objs[2]),
			}), rdr.snapshot(), "the last good content is served, the others are not blocked")

			// still being written: the same answer, one retry, no snapshot
			etag2 := manifestETagOnDisk(t, root)
			assert.Equal(t, []call{manifestCall(etag2), objCall(t, alpha2)}, rdr.roundCalls())
			assert.Nil(t, rdr.snapshot(), "no change of state, no snapshot")

			// the writer finishes in place
			require.NoError(t, os.WriteFile(objPath, []byte(alpha2.content), 0o600))

			assert.Equal(t, []call{manifestCall(etag2), objCall(t, alpha2)}, rdr.roundCalls())
			assert.Equal(t, snapOf(2, allOK(t, alpha2, objs[1], objs[2])), rdr.snapshot())
			assert.Empty(t, rdr.reader.Status().Failures)

			assert.Equal(t, []call{manifestCall(etag2)}, rdr.roundCalls(), "quiet again")
		})
	}
}

func TestFileReader_PartiallyWrittenManifestIsRejectedThenRecovers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	objs := threeObjects()
	publishFiles(t, root, 1, objs...)

	rdr := startFileRig(t, root)
	require.NotNil(t, rdr.snapshot())

	etag1 := manifestETagOnDisk(t, root)
	alpha2 := obj{name: nameAlpha, content: contentAlphaChanged}
	publishFiles(t, root, 2, alpha2, objs[1], objs[2])

	manifestPath := filepath.Join(root, filepath.FromSlash(manifestKey))
	full, err := os.ReadFile(manifestPath)
	require.NoError(t, err)

	// the control plane wrote the manifest in place and the poll came in the middle of it
	require.NoError(t, os.WriteFile(manifestPath, full[:len(full)/2], 0o600))

	assert.Equal(
		t,
		[]call{manifestCall(etag1)},
		rdr.roundCalls(),
		"the half manifest is read and nothing is fetched from it",
	)
	assert.Nil(t, rdr.snapshot())
	assert.Equal(t, manifestreader.OutcomeInvalid, rdr.reader.Status().Manifest.Outcome)
	assert.Equal(t, uint64(1), rdr.reader.Status().Manifest.Generation, "generation 1 is still held")

	require.NoError(t, os.WriteFile(manifestPath, full, 0o600))

	assert.Equal(t, []call{manifestCall(etagOf(full[:len(full)/2])), objCall(t, alpha2)}, rdr.roundCalls())
	assert.Equal(t, snapOf(2, allOK(t, alpha2, objs[1], objs[2])), rdr.snapshot())
	assert.Equal(t, manifestreader.OutcomeAccepted, rdr.reader.Status().Manifest.Outcome)
}

func TestFileReader_ManifestFileRemovedIsMissingThenRecovers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	objs := threeObjects()
	publishFiles(t, root, 1, objs...)

	rdr := startFileRig(t, root)
	require.NotNil(t, rdr.snapshot())

	manifestPath := filepath.Join(root, filepath.FromSlash(manifestKey))
	saved, err := os.ReadFile(manifestPath)
	require.NoError(t, err)

	etag := manifestETagOnDisk(t, root)

	require.NoError(t, os.Remove(manifestPath))

	assert.Equal(t, []call{manifestCall(etag)}, rdr.roundCalls())

	status := rdr.reader.Status()
	assert.Equal(t, manifestreader.PollNotFound, status.Poll.Result)
	assert.Equal(t, manifestreader.ReasonMissing, status.Poll.Reason)
	assert.Equal(t, accepted(1, 3), status.Manifest, "what was accepted stays")
	assert.Equal(t, manifestreader.Counts{OK: 3}, status.Counts)
	assert.Nil(t, rdr.snapshot())

	atomicWrite(t, manifestPath, saved)

	assert.Equal(t, []call{manifestCall(etag)}, rdr.roundCalls())
	assert.Equal(t, manifestreader.PollNotModified, rdr.reader.Status().Poll.Result)
	assert.Empty(t, rdr.reader.Status().Poll.Reason)
}

func TestFileReader_ObjectFileRemovedKeepsTheLastGoodContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	objs := threeObjects()
	publishFiles(t, root, 1, objs...)

	rdr := startFileRig(t, root)
	require.NotNil(t, rdr.snapshot())

	etag1 := manifestETagOnDisk(t, root)
	alpha2 := obj{name: nameAlpha, content: contentAlphaSecond}

	// generation 2 lists alpha2, whose file is not there
	atomicWrite(
		t,
		filepath.Join(root, filepath.FromSlash(manifestKey)),
		mustMarshal(t, manifestOf(t, 2, alpha2, objs[1], objs[2])),
	)

	assert.Equal(t, []call{manifestCall(etag1), objCall(t, alpha2)}, rdr.roundCalls())
	assert.Equal(t, snapOf(2, map[string]manifestreader.ObjectState{
		nameAlpha: staleAlpha(
			t,
			manifestreader.ReasonMissing,
		),
		nameBeta:  okState(t, objs[1]),
		nameGamma: okState(t, objs[2]),
	}), rdr.snapshot())

	atomicWrite(t, filepath.Join(root, filepath.FromSlash(keyOf(t, alpha2))), []byte(alpha2.content))

	assert.Equal(t, []call{manifestCall(manifestETagOnDisk(t, root)), objCall(t, alpha2)}, rdr.roundCalls())
	assert.Equal(t, snapOf(2, allOK(t, alpha2, objs[1], objs[2])), rdr.snapshot())
}

func TestFileReader_RestartWithStateDirectoryCostsOneConditionalRead(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	objs := threeObjects()
	publishFiles(t, root, 1, objs...)

	dir := filepath.Join(t.TempDir(), "state")

	state := openState(t, dir, nil)
	first := startFileRig(t, root, manifestreader.WithHooks(state.store.Hooks()))
	require.NotNil(t, first.snapshot())
	state.flush()
	first.stop()
	state.close()

	// the second run: the files are still there, a new process
	state2 := openState(t, dir, nil)
	second := startFileRig(t, root, manifestreader.WithHooks(state2.store.Hooks()))

	assert.Equal(t, snapOf(1, allOK(t, objs...)), second.snapshot(), "served from the state directory before any read")
	assert.Equal(
		t,
		[]call{manifestCall(manifestETagOnDisk(t, root))},
		second.rec.take(),
		"one conditional read, no object read",
	)
	assert.Equal(t, manifestreader.PollNotModified, second.reader.Status().Poll.Result)

	// and when the files changed while the relay was down: the manifest and the changed object
	second.stop()
	state2.close()

	alpha2 := obj{name: nameAlpha, content: contentAlphaChanged}
	etag1 := manifestETagOnDisk(t, root)
	publishFiles(t, root, 2, alpha2, objs[1], objs[2])

	state3 := openState(t, dir, nil)
	third := startFileRig(t, root, manifestreader.WithHooks(state3.store.Hooks()))

	assert.Equal(t, []call{manifestCall(etag1), objCall(t, alpha2)}, sortCalls(third.rec.take()))
}
