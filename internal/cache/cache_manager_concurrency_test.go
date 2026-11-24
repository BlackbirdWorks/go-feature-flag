package cache_test

import (
	"maps"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/modules/core/dto"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

// GetFlag and AllFlags are lock free and must see a complete cache while UpdateCache replaces it.
func Test_CacheManager_ReadWhileUpdating(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		flags int
		close bool
	}{
		{name: "10 flags", flags: 10},
		{name: "200 flags", flags: 200},
		{name: "readers racing with Close", flags: 10, close: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			flags := map[string]dto.DTO{}
			maps.Copy(flags, benchFlagsMap(t, tt.flags))
			m := cache.New(nil, "", &fflog.FFLogger{})
			require.NoError(t, m.UpdateCache(flags, nil, false))

			var stop atomic.Bool
			var wg sync.WaitGroup
			for range 4 {
				wg.Go(func() {
					for !stop.Load() {
						// Only a reader racing with Close may see the "not initialised" error.
						f, err := m.GetFlag("flag-1")
						if err == nil {
							assert.NotNil(t, f)
						} else if !tt.close {
							assert.NoError(t, err)
						}
						all, err := m.AllFlags()
						if err == nil {
							assert.Len(t, all, tt.flags)
						} else if !tt.close {
							assert.NoError(t, err)
						}
					}
				})
			}
			for range 50 {
				require.NoError(t, m.UpdateCache(flags, nil, false))
			}
			if tt.close {
				m.Close()
			}
			stop.Store(true)
			wg.Wait()
		})
	}
}
