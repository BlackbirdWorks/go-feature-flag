package cache_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/internal/notification"
	"github.com/blackbirdworks/semaphore/modules/core/dto"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/utils/fflog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thejerf/slogassert"
	"gopkg.in/yaml.v3"
)

func persistFlags(t *testing.T) map[string]dto.DTO {
	t.Helper()
	flags := map[string]dto.DTO{}
	require.NoError(t, yaml.Unmarshal([]byte(`f:
  variations:
    A: true
    B: false
  defaultRule:
    variation: A
`), &flags))
	return flags
}

func TestCacheManagerPersist(t *testing.T) {
	t.Parallel()
	const sentinel = "sentinel"
	tests := []struct {
		name        string
		fileInDir   func(dir string) string
		existing    bool
		flags       func(t *testing.T) map[string]dto.DTO
		wantMessage string
		wantContent func(t *testing.T, path string)
	}{
		{
			name:        "the flags are written to the persistent file",
			fileInDir:   func(dir string) string { return filepath.Join(dir, "persist.yaml") },
			flags:       persistFlags,
			wantMessage: "Flags cache persisted to file",
			wantContent: func(t *testing.T, path string) {
				b, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Contains(t, string(b), "variations")
			},
		},
		{
			name:        "an unchanged cache does not rewrite the file",
			fileInDir:   func(dir string) string { return filepath.Join(dir, "persist.yaml") },
			existing:    true,
			flags:       func(*testing.T) map[string]dto.DTO { return map[string]dto.DTO{} },
			wantMessage: "No change in the cache, skipping the persist",
			wantContent: func(t *testing.T, path string) {
				b, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, sentinel, string(b))
			},
		},
		{
			name:        "a file that cannot be written is logged",
			fileInDir:   func(dir string) string { return filepath.Join(dir, "missing-dir", "persist.yaml") },
			flags:       persistFlags,
			wantMessage: "Error while writing flags to file",
			wantContent: func(t *testing.T, path string) {
				_, err := os.Stat(path)
				assert.ErrorIs(t, err, os.ErrNotExist)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := tt.fileInDir(t.TempDir())
			if tt.existing {
				require.NoError(t, os.WriteFile(path, []byte(sentinel), 0o600))
			}
			handler := slogassert.New(t, slog.LevelDebug, nil)
			logger := &fflog.FFLogger{LeveledLogger: slog.New(handler)}
			m := cache.New(notification.NewService([]notifier.Notifier{}), path, logger)

			require.NoError(t, m.UpdateCache(tt.flags(t), logger, false))
			m.Close() // waits for the persistence goroutine

			handler.AssertMessage(tt.wantMessage)
			tt.wantContent(t, path)
		})
	}
}

func TestCacheManagerReadsAfterClose(t *testing.T) {
	t.Parallel()
	m := cache.New(notification.NewService([]notifier.Notifier{}), "", nil)
	m.Close()

	_, err := m.AllFlags()
	assert.ErrorContains(t, err, "before the initialisation")
	_, err = m.GetFlag("f")
	assert.ErrorContains(t, err, "before the initialisation")
}
