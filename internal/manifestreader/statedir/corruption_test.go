package statedir_test

import (
	"io/fs"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

func flipByte(data []byte) []byte {
	out := append([]byte(nil), data...)
	out[len(out)/2] ^= 0xff

	return out
}

func TestStore_RestoreIgnoresWhatDoesNotVerify(t *testing.T) {
	t.Parallel()

	const (
		alpha = `{contentAlpha:1}`
		beta  = `{contentBeta:2}`
		gamma = `{"gamma":3}`
	)

	full := wantRestored{etag: etagOne, manifest: manifestOne, objects: []string{alpha, beta, gamma}}
	without := func(content string) wantRestored {
		out := full
		out.objects = nil

		for _, obj := range full.objects {
			if obj != content {
				out.objects = append(out.objects, obj)
			}
		}

		return out
	}

	manifestFile, versionFile := manifestName, versionName

	tests := []struct {
		mutate        func(*memDisk)
		name          string
		wantRestore   string
		wantObjects   wantRestored
		wantDiscarded int
	}{
		{
			name: "intact", mutate: func(*memDisk) {},
			wantRestore: manifestreader.RestoreRestored, wantObjects: full,
		},
		{
			name: "an object truncated", wantRestore: manifestreader.RestorePartial, wantDiscarded: 1,
			wantObjects: without(beta),
			mutate: func(d *memDisk) {
				data, _ := d.read(objectFile(beta))
				d.write(objectFile(beta), data[:len(data)/2])
			},
		},
		{
			name: "an object emptied", wantRestore: manifestreader.RestorePartial, wantDiscarded: 1,
			wantObjects: without(beta),
			mutate:      func(d *memDisk) { d.write(objectFile(beta), nil) },
		},
		{
			name: "an object with a flipped byte", wantRestore: manifestreader.RestorePartial, wantDiscarded: 1,
			wantObjects: without(gamma),
			mutate: func(d *memDisk) {
				data, _ := d.read(objectFile(gamma))
				d.write(objectFile(gamma), flipByte(data))
			},
		},
		{
			name: "an object missing", wantRestore: manifestreader.RestorePartial, wantDiscarded: 1,
			wantObjects: without(alpha),
			mutate:      func(d *memDisk) { d.remove(objectFile(alpha)) },
		},
		{
			name: "the content of one object under the name of another", wantRestore: manifestreader.RestorePartial,
			wantDiscarded: 1, wantObjects: without(alpha),
			mutate: func(d *memDisk) {
				data, _ := d.read(objectFile(beta))
				d.write(objectFile(alpha), data)
			},
		},
		{
			name: "all objects gone: the manifest alone is restored", wantRestore: manifestreader.RestorePartial,
			wantDiscarded: 3, wantObjects: wantRestored{etag: etagOne, manifest: manifestOne},
			mutate: func(d *memDisk) {
				for _, obj := range full.objects {
					d.remove(objectFile(obj))
				}
			},
		},
		{
			name: "files nobody wrote are ignored", wantRestore: manifestreader.RestoreRestored, wantObjects: full,
			mutate: func(d *memDisk) {
				d.write("objects/README", []byte("hello"))
				d.write(objectFile("orphan"), []byte("orphan"))
				d.write(".tmp-7", []byte("half"))
			},
		},
		{
			name: "manifest copy with a flipped byte", wantRestore: manifestreader.RestoreEmpty,
			mutate: func(d *memDisk) {
				data, _ := d.read(manifestFile)
				d.write(manifestFile, flipByte(data))
			},
		},
		{
			name: "manifest copy truncated", wantRestore: manifestreader.RestoreEmpty,
			mutate: func(d *memDisk) {
				data, _ := d.read(manifestFile)
				d.write(manifestFile, data[:len(data)/2])
			},
		},
		{
			name: "manifest copy that is not json", wantRestore: manifestreader.RestoreEmpty,
			mutate: func(d *memDisk) { d.write(manifestFile, []byte("not json")) },
		},
		{
			name: "manifest copy with another etag but the old digest", wantRestore: manifestreader.RestoreEmpty,
			mutate: func(d *memDisk) {
				data, _ := d.read(manifestFile)
				d.write(manifestFile, []byte(strings.Replace(string(data), `\"e1\"`, `\"e2\"`, 1)))
			},
		},
		{
			name: "manifest copy missing", wantRestore: manifestreader.RestoreEmpty,
			mutate: func(d *memDisk) { d.remove(manifestFile) },
		},
		{
			name: "another layout version", wantRestore: manifestreader.RestoreEmpty,
			mutate: func(d *memDisk) { d.write(versionFile, []byte("semaphore-manifest-state 2\n")) },
		},
		{
			name: "layout version file missing", wantRestore: manifestreader.RestoreEmpty,
			mutate: func(d *memDisk) { d.remove(versionFile) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disk := newMemDisk()
			fsys := &memFS{disk: disk, faults: &faults{}}

			first := openStore(t, fsys, statedirConfig())
			first.saveAndFlush(persistOf(etagOne, manifestOne, alpha, beta, gamma))
			first.close()

			tt.mutate(disk)

			restored, status, err := restoreOf(t, fsys)

			assert.Equal(t, tt.wantRestore, status.Restore)
			assert.Equal(t, tt.wantDiscarded, status.DiscardedObjects)

			if tt.wantRestore == manifestreader.RestoreEmpty {
				require.ErrorIs(t, err, manifestreader.ErrNothingRestored)
				assert.Nil(t, restored)

				return
			}

			require.NoError(t, err)
			assertRestored(t, tt.wantObjects, restored)
		})
	}
}

func TestStore_RestoreOfAnUnreadableDirectory(t *testing.T) {
	t.Parallel()

	const alpha = `{contentAlpha:1}`

	tests := []struct {
		failRead    map[string]error
		name        string
		wantRestore string
		wantObjects []string
		wantErr     bool
	}{
		{
			name: "the version file cannot be read", failRead: map[string]error{versionName: syscall.EIO},
			wantRestore: manifestreader.RestoreFailed, wantErr: true,
		},
		{
			name: "the manifest copy cannot be read", failRead: map[string]error{manifestName: fs.ErrPermission},
			wantRestore: manifestreader.RestoreFailed, wantErr: true,
		},
		{
			name:        "one object cannot be read: it is fetched again",
			failRead:    map[string]error{objectFile(alpha): syscall.EIO},
			wantRestore: manifestreader.RestorePartial,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disk := newMemDisk()
			fsys := &memFS{disk: disk, faults: &faults{}}

			first := openStore(t, fsys, statedirConfig())
			first.saveAndFlush(persistOf(etagOne, manifestOne, alpha))
			first.close()

			broken := &memFS{disk: disk, faults: &faults{failRead: tt.failRead}}
			restored, status, err := restoreOf(t, broken)

			assert.Equal(t, tt.wantRestore, status.Restore)

			if tt.wantErr {
				require.Error(t, err)
				require.NotErrorIs(t, err, manifestreader.ErrNothingRestored)
				assert.Nil(t, restored)

				return
			}

			require.NoError(t, err)
			assertRestored(t, wantRestored{etag: etagOne, manifest: manifestOne}, restored)
		})
	}
}

func TestStore_SaveAfterAnotherLayoutVersionStartsOver(t *testing.T) {
	t.Parallel()

	disk := newMemDisk()
	fsys := &memFS{disk: disk, faults: &faults{}}

	// a directory of a later layout: its files are not ours to read, and the next save replaces them
	disk.write(versionName, []byte("semaphore-manifest-state 2\n"))
	disk.write(manifestName, []byte("future format"))
	disk.write(objectFile("future"), []byte("future"))
	disk.write("objects/README", []byte("kept"))

	run := openStore(t, fsys, statedirConfig())

	_, err := run.store.Hooks().Restore()
	require.ErrorIs(t, err, manifestreader.ErrNothingRestored)

	run.saveAndFlush(persistOf(etagOne, manifestOne, contentAlpha))
	run.close()

	assert.Equal(t, []string{versionName, manifestName, objectFile(contentAlpha), "objects/README"}, disk.names())

	restored, _, err := restoreOf(t, fsys)
	require.NoError(t, err)
	assertRestored(t, wantRestored{etag: etagOne, manifest: manifestOne, objects: []string{contentAlpha}}, restored)
}
