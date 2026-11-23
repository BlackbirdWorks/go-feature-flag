package cache_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomaspoignant/go-feature-flag/internal/cache"
	"github.com/thomaspoignant/go-feature-flag/modules/core/dto"
	"github.com/thomaspoignant/go-feature-flag/utils/fflog"
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
			for k, v := range benchFlagsMap(t, tt.flags) {
				flags[k] = v
			}
			m := cache.New(nil, "", &fflog.FFLogger{})
			require.NoError(t, m.UpdateCache(flags, nil, false))

			var stop atomic.Bool
			var wg sync.WaitGroup
			for r := 0; r < 4; r++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for !stop.Load() {
						f, err := m.GetFlag("flag-1")
						if err == nil {
							assert.NotNil(t, f)
						}
						all, err := m.AllFlags()
						if err == nil {
							assert.Len(t, all, tt.flags)
						}
					}
				}()
			}
			for i := 0; i < 50; i++ {
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
