package cache_test

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/model/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/utils/fflog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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
		name   string
		mutate func(f *flag.InternalFlag)
	}{
		{name: "variation value", mutate: func(f *flag.InternalFlag) {
			v := *(*f.Variations)["A"]
			v.(map[string]any)["k"].([]any)[2].(map[string]any)["deep"] = "hacked"
			v.(map[string]any)["k"].([]any)[0] = "hacked"
		}},
		{name: "variation replaced", mutate: func(f *flag.InternalFlag) {
			x := any("hacked")
			*(*f.Variations)["B"] = x
			(*f.Variations)["C"] = &x
		}},
		{name: "metadata", mutate: func(f *flag.InternalFlag) {
			(*f.Metadata)["owner"].(map[string]any)["team"].([]any)[0] = "hacked"
		}},
		{name: "targeting rule", mutate: func(f *flag.InternalFlag) {
			r := &(*f.Rules)[0]
			*r.Query = "hacked"
			*r.VariationResult = "hacked"
			(*r.Percentages)["A"] = 1
			*r.ProgressiveRollout.End.Percentage = 5
			*r.ProgressiveRollout.End.Variation = "hacked"
			*r.Name = "hacked"
		}},
		{name: "default rule", mutate: func(f *flag.InternalFlag) { (*f.DefaultRule.Percentages)["A"] = 99 }},
		{name: "experimentation", mutate: func(f *flag.InternalFlag) { *f.Experimentation.Start = f.Experimentation.End.Add(1) }},
		{name: "scheduled rollout", mutate: func(f *flag.InternalFlag) {
			step := &(*f.Scheduled)[0]
			(*step.Metadata)["owner"].(map[string]any)["team"].([]any)[0] = "hacked"
			(*step.Variations)["A"] = nil
			*step.Date = step.Date.Add(1)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var flags map[string]dto.DTO
			require.NoError(t, yaml.Unmarshal([]byte(nestedFlagYAML), &flags))
			m := cache.New(nil, "", &fflog.FFLogger{})
			require.NoError(t, m.UpdateCache(flags, nil, false))
			live, err := m.GetFlag("nested")
			require.NoError(t, err)
			before, err := json.Marshal(live)
			require.NoError(t, err)

			snap, err := m.AllFlags()
			require.NoError(t, err)
			tt.mutate(snap["nested"].(*flag.InternalFlag))

			live, err = m.GetFlag("nested")
			require.NoError(t, err)
			after, err := json.Marshal(live)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after))
		})
	}
}
