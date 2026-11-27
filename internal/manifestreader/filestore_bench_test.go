//go:build bench

package manifestreader_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

const benchFile = "manifest.yaml"

// BenchmarkFileStore_Poll measures what one poll of the manifest costs: an unchanged file (the conditional read
// answered with "not modified"), a changed one (hashed, then read), an unconditional read, and as the floor of any
// validator the stat of the file. Sizes: a small manifest, a typical one, one of 20000 entries, the default cap.
func BenchmarkFileStore_Poll(b *testing.B) {
	for _, size := range []int{1 << 10, 100 << 10, 4 << 20, 16 << 20} {
		dir := b.TempDir()
		path := filepath.Join(dir, benchFile)
		require.NoError(b, os.WriteFile(path, pseudoBytes(size), 0o600))

		store, err := manifestreader.NewFileStore(dir)
		require.NoError(b, err)

		req := manifestreader.GetRequest{Key: benchFile, Limit: 16 << 20}
		first, err := store.Get(b.Context(), req)
		require.NoError(b, err)

		name := fmt.Sprintf("%dKiB", size>>10)
		cond := req
		cond.ETag = first.ETag

		b.Run("unchanged/"+name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, getErr := store.Get(b.Context(), cond)
				require.ErrorIs(b, getErr, manifestreader.ErrNotModified)
			}
		})

		stale := req
		stale.ETag = `"stale"`

		b.Run("changed/"+name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, getErr := store.Get(b.Context(), stale)
				require.NoError(b, getErr)
			}
		})

		b.Run("unconditional/"+name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, getErr := store.Get(b.Context(), req)
				require.NoError(b, getErr)
			}
		})

		b.Run("stat-only-floor/"+name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, statErr := os.Stat(path)
				require.NoError(b, statErr)
			}
		})
	}
}

func pseudoBytes(size int) []byte {
	out := make([]byte, size)
	state := uint64(88172645463325252)

	for i := range out {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		out[i] = byte(state)
	}

	return out
}
