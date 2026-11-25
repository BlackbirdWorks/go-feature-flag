package cache_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/model/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	flagOne = "flag-1"
	hacked  = "hacked"
)

// Test_AllFlags_ReturnsDetachedFlags checks that the flags returned by the public snapshot
// (AllFlags, which feeds GetFlagsFromCache and the notifier diff) are copies: mutating them must
// not change what the cache serves to the evaluations or to the next snapshot.
func Test_AllFlags_ReturnsDetachedFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want   any
		mutate func(f *flag.InternalFlag)
		read   func(f flag.Flag) any
		name   string
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
				v := hacked
				f.Version = &v
			},
			read: func(f flag.Flag) any { return f.GetVersion() },
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mgr := cache.New(nil, "", slog.New(slog.DiscardHandler))
			require.NoError(t, mgr.UpdateCache(benchFlagsMap(t, 3), nil, false))

			snap, err := mgr.AllFlags()
			require.NoError(t, err)

			f, ok := snap[flagOne].(*flag.InternalFlag)
			require.True(t, ok)
			tt.mutate(f)
			delete(snap, "flag-2")

			live, err := mgr.GetFlag(flagOne)
			require.NoError(t, err)
			assert.Equal(t, tt.want, tt.read(live))

			again, err := mgr.AllFlags()
			require.NoError(t, err)
			assert.Len(t, again, 3)
			assert.Equal(t, tt.want, tt.read(again[flagOne]))
		})
	}
}

// Test_AllFlags_HeldSnapshotWhileUpdating holds snapshots and mutates them while the cache is
// replaced concurrently (meant to run with -race).
func Test_AllFlags_HeldSnapshotWhileUpdating(t *testing.T) {
	t.Parallel()

	mgr := cache.New(nil, "", slog.New(slog.DiscardHandler))
	flags := benchFlagsMap(t, 5)
	require.NoError(t, mgr.UpdateCache(flags, nil, false))

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 200 {
				snap, err := mgr.AllFlags()
				if err != nil {
					return
				}

				for _, f := range snap {
					d := true
					if iflag, ok := f.(*flag.InternalFlag); assert.True(t, ok) {
						iflag.Disable = &d
					}
				}

				live, getFlagErr := mgr.GetFlag(flagOne)
				if getFlagErr == nil {
					_ = live.IsDisable()
				}
			}
		})
	}

	for range 50 {
		require.NoError(t, mgr.UpdateCache(flags, nil, false))
	}

	wg.Wait()

	live, err := mgr.GetFlag(flagOne)
	require.NoError(t, err)
	assert.False(t, live.IsDisable())
}

const nestedFlagYAML = `nested:
  variations:
    A: {k: [1, 2, {deep: x}]}
    B: {k: []}
  metadata:
    owner: {team: [a, b]}
  targeting:
    - name: r1
      query: key eq "u"
      variation: A
      percentage: {A: 60, B: 40}
      progressiveRollout:
        initial: {variation: A, percentage: 0, date: 2021-01-01T00:00:00Z}
        end: {variation: B, percentage: 100, date: 2022-01-01T00:00:00Z}
  defaultRule:
    percentage: {A: 50, B: 50}
  experimentation:
    start: 2021-01-01T00:00:00Z
    end: 2022-01-01T00:00:00Z
  scheduledRollout:
    - date: 2021-06-01T00:00:00Z
      variations: {A: {k: [9]}, B: {k: []}}
      metadata: {owner: {team: [c]}}
`

// Test_AllFlags_ReturnsDeepCopies checks that the nested data of the flags of the public snapshot
// (variations, rules, metadata, rollouts) is not shared with the live flags.
func Test_AllFlags_ReturnsDeepCopies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mutate func(t *testing.T, f *flag.InternalFlag)
		name   string
		// wantLiveChanged is whether the live flag changed after the snapshot was mutated
		wantLiveChanged bool
	}{
		{name: "variation value", wantLiveChanged: false, mutate: func(t *testing.T, f *flag.InternalFlag) {
			t.Helper()

			v := *(*f.Variations)["A"]
			m, ok := v.(map[string]any)
			require.True(t, ok)
			kSlice, ok := m["k"].([]any)
			require.True(t, ok)
			nested, ok := kSlice[2].(map[string]any)
			require.True(t, ok)

			nested["deep"] = hacked
			kSlice[0] = hacked
		}},
		{name: "variation replaced", wantLiveChanged: false, mutate: func(_ *testing.T, f *flag.InternalFlag) {
			x := any(hacked)
			*(*f.Variations)["B"] = x
			(*f.Variations)["C"] = &x
		}},
		{name: "metadata", wantLiveChanged: false, mutate: func(t *testing.T, f *flag.InternalFlag) {
			t.Helper()

			owner, ok := (*f.Metadata)["owner"].(map[string]any)
			require.True(t, ok)
			team, ok := owner["team"].([]any)
			require.True(t, ok)

			team[0] = hacked
		}},
		{name: "targeting rule", wantLiveChanged: false, mutate: func(_ *testing.T, f *flag.InternalFlag) {
			r := &(*f.Rules)[0]
			*r.Query = hacked
			*r.VariationResult = hacked
			(*r.Percentages)["A"] = 1
			*r.ProgressiveRollout.End.Percentage = 5
			*r.ProgressiveRollout.End.Variation = hacked
			*r.Name = hacked
		}},
		{
			name:            "default rule",
			wantLiveChanged: false,
			mutate:          func(_ *testing.T, f *flag.InternalFlag) { (*f.DefaultRule.Percentages)["A"] = 99 },
		},
		{
			name:            "experimentation",
			wantLiveChanged: false,
			mutate: func(_ *testing.T, f *flag.InternalFlag) {
				*f.Experimentation.Start = f.Experimentation.End.Add(1)
			},
		},
		{name: "scheduled rollout", wantLiveChanged: false, mutate: func(t *testing.T, f *flag.InternalFlag) {
			t.Helper()

			step := &(*f.Scheduled)[0]
			owner, ok := (*step.Metadata)["owner"].(map[string]any)
			require.True(t, ok)
			team, ok := owner["team"].([]any)
			require.True(t, ok)

			team[0] = hacked
			(*step.Variations)["A"] = nil
			*step.Date = step.Date.Add(1)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var flags map[string]dto.DTO
			require.NoError(t, yaml.Unmarshal([]byte(nestedFlagYAML), &flags))

			mgr := cache.New(nil, "", slog.New(slog.DiscardHandler))
			require.NoError(t, mgr.UpdateCache(flags, nil, false))
			live, err := mgr.GetFlag(fixtures.NestedKey)
			require.NoError(t, err)
			before, err := json.Marshal(live)
			require.NoError(t, err)

			snap, err := mgr.AllFlags()
			require.NoError(t, err)

			iflag, ok := snap[fixtures.NestedKey].(*flag.InternalFlag)
			require.True(t, ok)
			tt.mutate(t, iflag)

			live, err = mgr.GetFlag(fixtures.NestedKey)
			require.NoError(t, err)
			after, err := json.Marshal(live)
			require.NoError(t, err)
			assert.Equal(
				t,
				tt.wantLiveChanged,
				!bytes.Equal(before, after),
				"the live flag is not changed through the snapshot",
			)
		})
	}
}

// Test_AllFlagsReadOnly_ReturnsTheLiveFlags checks that the copy-free accessor serves the flags of the cache
// (same pointers as the evaluation lookup) and fails before the initialisation.
func Test_AllFlagsReadOnly_ReturnsTheLiveFlags(t *testing.T) {
	t.Parallel()

	mgr := cache.New(nil, "", slog.New(slog.DiscardHandler))
	mgr.Close()
	_, err := mgr.AllFlagsReadOnly()
	require.Error(t, err)

	mgr = cache.New(nil, "", slog.New(slog.DiscardHandler))
	require.NoError(t, mgr.UpdateCache(benchFlagsMap(t, 3), nil, false))
	shared, err := mgr.AllFlagsReadOnly()
	require.NoError(t, err)
	require.Len(t, shared, 3)

	live, err := mgr.GetFlag(flagOne)
	require.NoError(t, err)
	assert.Same(t, live, shared[flagOne])
}
