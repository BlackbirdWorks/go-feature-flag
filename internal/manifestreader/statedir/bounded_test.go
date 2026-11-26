package statedir_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

// TestStore_RestoreDiscardsWhatIsNotWhatItWasSaved runs on the seam: a name that is not a regular file, a file above
// its limit and lists above their bounds are discarded, never read past their limit.
func TestStore_RestoreDiscardsWhatIsNotWhatItWasSaved(t *testing.T) {
	t.Parallel()

	tests := []struct {
		damage      func(*memDisk)
		name        string
		wantRestore string
		wantObjects []string
		cfg         statedir.Config
	}{
		{
			name: "an object that is not a regular file", wantRestore: manifestreader.RestorePartial,
			damage:      func(d *memDisk) { d.irregular[objectFile(contentBeta)] = true },
			wantObjects: []string{contentAlpha},
		},
		{
			name: "an object larger than its recorded size", wantRestore: manifestreader.RestorePartial,
			damage:      func(d *memDisk) { d.write(objectFile(contentBeta), []byte(strings.Repeat("b", 1<<20))) },
			wantObjects: []string{contentAlpha},
		},
		{
			name: "an object smaller than its recorded size", wantRestore: manifestreader.RestorePartial,
			damage:      func(d *memDisk) { d.write(objectFile(contentBeta), []byte("b")) },
			wantObjects: []string{contentAlpha},
		},
		{
			name: "a manifest copy that is not a regular file", wantRestore: manifestreader.RestoreEmpty,
			damage: func(d *memDisk) { d.irregular[manifestName] = true },
		},
		{
			name: "a manifest copy above its limit", wantRestore: manifestreader.RestoreEmpty,
			cfg:    statedir.Config{MaxManifestBytes: 1 << 10, MaxObjects: 4},
			damage: func(d *memDisk) { d.write(manifestName, []byte(strings.Repeat("x", 1<<20))) },
		},
		{
			name: "more objects than the bound", wantRestore: manifestreader.RestoreEmpty,
			cfg: statedir.Config{MaxObjects: 1}, damage: func(*memDisk) {},
		},
		{
			name: "an object above the object bound", wantRestore: manifestreader.RestoreEmpty,
			cfg: statedir.Config{MaxObjectBytes: 3}, damage: func(*memDisk) {},
		},
		{
			name: "within every bound", wantRestore: manifestreader.RestoreRestored,
			cfg:         statedir.Config{MaxManifestBytes: 1 << 10, MaxObjectBytes: 64, MaxObjects: 2},
			damage:      func(*memDisk) {},
			wantObjects: []string{contentAlpha, contentBeta},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disk := newMemDisk()
			fsys := &memFS{disk: disk, faults: &faults{}}

			first := openStore(t, fsys, statedirConfig())
			first.saveAndFlush(persistOf(etagOne, manifestOne, contentAlpha, contentBeta))
			first.close()

			tt.damage(disk)

			cfg := tt.cfg
			cfg.FS, cfg.Dir = fsys, stateDirName

			store, err := statedir.New(cfg)
			require.NoError(t, err)

			defer func() { require.NoError(t, store.Close()) }()

			restored, err := store.Hooks().Restore()
			assert.Equal(t, tt.wantRestore, store.Hooks().StateStatus().Restore)

			if tt.wantRestore == manifestreader.RestoreEmpty {
				require.ErrorIs(t, err, manifestreader.ErrNothingRestored)

				return
			}

			require.NoError(t, err)
			assertRestored(t, wantRestored{etag: etagOne, manifest: manifestOne, objects: tt.wantObjects}, restored)
		})
	}
}
