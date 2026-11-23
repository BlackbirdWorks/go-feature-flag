package cache_test

import (
	"sync"
	"testing"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/utils/fflog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test_AllFlags_ReturnsDetachedFlags checks that the flags returned by the public snapshot
// (AllFlags, which feeds GetFlagsFromCache and the notifier diff) are copies: mutating them must
// not change what the cache serves to the evaluations or to the next snapshot.
func Test_AllFlags_ReturnsDetachedFlags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(f *flag.InternalFlag)
		read   func(f flag.Flag) any
		want   any
	}{
		{
			name: "disable the flag in the snapshot",
			mutate: func(f *flag.InternalFlag) {
				d := true
				f.Disable = &d
			},
			read: func(f flag.Flag) any { return f.IsDisable() },
			want: false,
		},
		{
			name: "change the version in the snapshot",
			mutate: func(f *flag.InternalFlag) {
				v := "hacked"
				f.Version = &v
			},
			read: func(f flag.Flag) any { return f.GetVersion() },
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := cache.New(nil, "", &fflog.FFLogger{})
			require.NoError(t, m.UpdateCache(benchFlagsMap(t, 3), nil, false))

			snap, err := m.AllFlags()
			require.NoError(t, err)
			f, ok := snap["flag-1"].(*flag.InternalFlag)
			require.True(t, ok)
			tt.mutate(f)
			delete(snap, "flag-2")

			live, err := m.GetFlag("flag-1")
			require.NoError(t, err)
			assert.Equal(t, tt.want, tt.read(live))

			again, err := m.AllFlags()
			require.NoError(t, err)
			assert.Len(t, again, 3)
			assert.Equal(t, tt.want, tt.read(again["flag-1"]))
		})
	}
}

// Test_AllFlags_HeldSnapshotWhileUpdating holds snapshots and mutates them while the cache is
// replaced concurrently (meant to run with -race).
func Test_AllFlags_HeldSnapshotWhileUpdating(t *testing.T) {
	t.Parallel()
	m := cache.New(nil, "", &fflog.FFLogger{})
	flags := benchFlagsMap(t, 5)
	require.NoError(t, m.UpdateCache(flags, nil, false))

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				snap, err := m.AllFlags()
				if err != nil {
					return
				}
				for _, f := range snap {
					d := true
					f.(*flag.InternalFlag).Disable = &d
				}
				if live, err := m.GetFlag("flag-1"); err == nil {
					_ = live.IsDisable()
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		require.NoError(t, m.UpdateCache(flags, nil, false))
	}
	wg.Wait()

	live, err := m.GetFlag("flag-1")
	require.NoError(t, err)
	assert.False(t, live.IsDisable())
}
