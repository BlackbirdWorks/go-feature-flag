package flag

import (
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
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

	base := time.Now()
	tests := []struct {
		name        string
		offset      time.Duration
		wantVar     string
		wantVersion string
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
	f := scheduledFlag(time.Now())
	key := weak.Make(f.Scheduled)
	_, err := f.applyScheduledRolloutSteps(svc, time.Now())
	require.NoError(t, err)
	_, ok := svc.scheduled.entries.Load(key)
	require.True(t, ok)
	// f is not used past this point, so the flag (and its Scheduled) is unreachable and collectable.
	assert.Eventually(t, func() bool {
		runtime.GC()
		_, ok := svc.scheduled.entries.Load(key)

		return !ok
	}, 5*time.Second, 20*time.Millisecond)
}

// the value handed to the caller belongs to the evaluation: changing it must not change the
// result of the next evaluations (the flag is memoized and shared).
func TestInternalFlag_ScheduledSteps_ReturnedValueIsNotShared(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		value  func() any
		mutate func(v any)
	}{
		{
			name:  "object",
			value: func() any { return map[string]any{"theme": "light", "nested": map[string]any{"a": "b"}} },
			mutate: func(v any) {
				m := v.(map[string]any)
				m["theme"] = "dark"
				m["nested"].(map[string]any)["a"] = "changed"
			},
		},
		{
			name:  "array",
			value: func() any { return []any{"a", map[string]any{"k": "v"}, []any{"x"}} },
			mutate: func(v any) {
				a := v.([]any)
				a[0] = "changed"
				a[1].(map[string]any)["k"] = "changed"
				a[2].([]any)[0] = "changed"
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			base := time.Now()
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
			first, _ := f.Value("flag", ctx, Context{})
			tt.mutate(first)
			second, _ := f.Value("flag", ctx, Context{})
			assert.Equal(t, tt.value(), second)
		})
	}
}
