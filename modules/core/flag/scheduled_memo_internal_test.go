package flag

import (
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/testutils/clocktest"
)

func scheduledFlag(base time.Time) *InternalFlag {
	f := &InternalFlag{
		Variations: &map[string]*any{
			"A": new(any("a")),
			"B": new(any("b")),
		},
		DefaultRule: &Rule{VariationResult: new("A")},
		Scheduled: &[]ScheduledStep{
			{Date: new(base.Add(2 * time.Hour)),
				DefaultRule: &Rule{VariationResult: new("B")}},
			{Date: new(base.Add(time.Hour)),
				Version: new("v2")},
		},
	}

	return f
}

func TestInternalFlag_ScheduledSteps_ResultFollowsDueSteps(t *testing.T) {
	t.Parallel()

	base := clocktest.Instant()
	tests := []struct {
		name        string
		wantVar     string
		wantVersion string
		offset      time.Duration
	}{
		{name: "no step due", offset: 0, wantVar: "A", wantVersion: ""},
		{name: "one step due (steps are not in date order)", offset: 90 * time.Minute, wantVar: "A", wantVersion: "v2"},
		{name: "both steps due", offset: 3 * time.Hour, wantVar: "B", wantVersion: "v2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := NewService()
			f := scheduledFlag(base)
			before := *f.Scheduled
			runParallel(200, func(int) {
				ctx := ffcontext.NewEvaluationContextBuilder("user").Build()
				ctx.AddCustomAttribute("gofeatureflag", map[string]any{
					"currentDateTime": base.Add(tt.offset).Format(time.RFC3339Nano)})
				v, d := f.Value("flag", ctx, Context{Service: svc})
				assert.Equal(t, tt.wantVar == "A", v == "a")
				assert.Equal(t, tt.wantVar, d.Variant)
			})
			res, err := f.applyScheduledRolloutSteps(svc, base.Add(tt.offset))
			require.NoError(t, err)
			assert.Equal(t, tt.wantVersion, res.GetVersion())
			// the flag itself is never modified
			assert.Empty(t, f.GetVersion())
			assert.Equal(t, "A", f.GetDefaultRule().GetVariationResult())
			assert.Equal(t, before, *f.Scheduled)
		})
	}
}

// the memoized flags must not outlive the flag they were built for.
func TestScheduledMemo_ReleasesEntriesOfCollectedFlags(t *testing.T) {
	t.Parallel()
	svc := NewService()
	f := scheduledFlag(clocktest.Instant())
	key := weak.Make(f.Scheduled)
	_, err := f.applyScheduledRolloutSteps(svc, clocktest.Instant())
	require.NoError(t, err)
	_, ok := svc.scheduled.entries.Load(key)
	require.True(t, ok)
	// f is not used past this point, so the flag (and its Scheduled) is unreachable and collectable.
	assert.Eventually(t, func() bool {
		runtime.GC()
		_, stillThere := svc.scheduled.entries.Load(key)

		return !stillThere
	}, 5*time.Second, 20*time.Millisecond)
}

// the value handed to the caller belongs to the evaluation: changing it must not change the
// result of the next evaluations (the flag is memoized and shared).
func TestInternalFlag_ScheduledSteps_ReturnedValueIsNotShared(t *testing.T) {
	t.Parallel()
	tests := []struct {
		mutate     func(t *testing.T, v any)
		value      func() any
		// wantSecond is the value the second evaluation returns: the mutation of the first one is not shared
		wantSecond func() any
		name       string
	}{
		{
			name:       "object",
			value:      func() any { return map[string]any{"theme": "light", "nested": map[string]any{"a": "b"}} },
			wantSecond: func() any { return map[string]any{"theme": "light", "nested": map[string]any{"a": "b"}} },
			mutate: func(t *testing.T, v any) {
				t.Helper()
				m, ok := v.(map[string]any)
				require.True(t, ok)
				m["theme"] = "dark"
				nested, ok := m["nested"].(map[string]any)
				require.True(t, ok)
				nested["a"] = "changed"
			},
		},
		{
			name:       "array",
			value:      func() any { return []any{"a", map[string]any{"k": "v"}, []any{"x"}} },
			wantSecond: func() any { return []any{"a", map[string]any{"k": "v"}, []any{"x"}} },
			mutate: func(t *testing.T, v any) {
				t.Helper()
				a, ok := v.([]any)
				require.True(t, ok)
				a[0] = "changed"
				elem1, ok := a[1].(map[string]any)
				require.True(t, ok)
				elem1["k"] = "changed"
				elem2, ok := a[2].([]any)
				require.True(t, ok)
				elem2[0] = "changed"
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			base := clocktest.Instant()
			val := tt.value()
			f := &InternalFlag{
				Variations:  &map[string]*any{"A": &val},
				DefaultRule: &Rule{VariationResult: new("A")},
				Scheduled: &[]ScheduledStep{{
					Date:    new(base.Add(-time.Hour)),
					Version: new("v2"),
				}},
			}
			ctx := ffcontext.NewEvaluationContextBuilder("user").Build()
			first, _ := f.Value("flag", ctx, Context{Service: NewService()})
			tt.mutate(t, first)
			second, _ := f.Value("flag", ctx, Context{Service: NewService()})
			assert.Equal(t, tt.wantSecond(), second)
		})
	}
}
