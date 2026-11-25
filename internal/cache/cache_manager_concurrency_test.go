package cache_test

import (
	"log/slog"
	"maps"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/modules/core/dto"
)

// GetFlag and AllFlags are lock free and must see a complete cache while UpdateCache replaces it.
func Test_CacheManager_ReadWhileUpdating(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		flags     int
		close     bool
		wantFlags int
	}{
		{name: "10 flags", flags: 10, wantFlags: 10},
		{name: "200 flags", flags: 200, wantFlags: 200},
		{name: "readers racing with Close", flags: 10, close: true, wantFlags: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flags := map[string]dto.DTO{}
			maps.Copy(flags, benchFlagsMap(t, tt.flags))

			mgr := cache.New(nil, "", slog.New(slog.DiscardHandler))
			require.NoError(t, mgr.UpdateCache(flags, nil, false))

			var (
				stop atomic.Bool
				wg   sync.WaitGroup
			)
			for range 4 {
				wg.Go(func() {
					for !stop.Load() {
						// Only a reader racing with Close may see the "not initialised" error.
						f, err := mgr.GetFlag(flagOne)
						if err == nil {
							assert.NotNil(t, f)
						} else if !tt.close {
							//nolint:testifylint // called inside worker goroutine where require is unsafe
							assert.NoError(t, err)
						}

						all, err := mgr.AllFlags()
						if err == nil {
							assert.Len(t, all, tt.wantFlags)
						} else if !tt.close {
							assert.NoError(t, err)
						}
					}
				})
			}

			for range 50 {
				require.NoError(t, mgr.UpdateCache(flags, nil, false))
			}

			if tt.close {
				mgr.Close()
			}

			stop.Store(true)
			wg.Wait()
		})
	}
}
