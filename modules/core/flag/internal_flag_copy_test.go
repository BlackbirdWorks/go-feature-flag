package flag_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

func TestInternalFlag_DeepCopy(t *testing.T) {
	t.Parallel()
	ptr := func(v any) *any { return &v }
	tests := []struct {
		name  string
		value func() any
		// mutate changes the copied value in place.
		mutate func(v any)
		// wantOriginalUnchanged is whether the original equals what it was built as after the copy was mutated
		wantOriginalUnchanged bool
	}{
		{name: "nil", wantOriginalUnchanged: true, value: func() any { return nil }, mutate: func(any) {}},
		{name: "scalar", wantOriginalUnchanged: true, value: func() any { return "a" }, mutate: func(any) {}},
		{
			name:                  "map of any",
			wantOriginalUnchanged: true,
			value:                 func() any { return map[string]any{"a": []any{1}} },
			mutate: func(v any) {
				v.(map[string]any)["a"].([]any)[0] = 2
			},
		},
		{
			name:                  "typed slice",
			wantOriginalUnchanged: true,
			value:                 func() any { return []string{"a"} },
			mutate:                func(v any) { v.([]string)[0] = "b" },
		},
		{
			name:                  "typed map",
			wantOriginalUnchanged: true,
			value:                 func() any { return map[string]string{"a": "a"} },
			mutate:                func(v any) { v.(map[string]string)["a"] = "b" },
		},
		{
			name:                  "typed map of slices",
			wantOriginalUnchanged: true,
			value:                 func() any { return map[string][]int{"a": {1}} },
			mutate: func(v any) {
				v.(map[string][]int)["a"][0] = 2
			},
		},
		{
			name:                  "nil map and slice",
			wantOriginalUnchanged: true,
			value:                 func() any { return map[string]any{"m": map[string]string(nil), "s": []int(nil), "n": nil} },
			mutate:                func(any) {},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			build := func() *flag.InternalFlag {
				return &flag.InternalFlag{
					Variations: &map[string]*any{"A": ptr(tt.value()), "nil": nil},
					Metadata:   &map[string]any{"m": tt.value()},
				}
			}
			orig := build()
			cp := orig.DeepCopy()
			assert.Equal(t, orig, cp)
			tt.mutate(*(*cp.Variations)["A"])
			tt.mutate((*cp.Metadata)["m"])
			assert.Equal(
				t,
				tt.wantOriginalUnchanged,
				assert.ObjectsAreEqual(build(), orig),
				"changing the copy must not change the original",
			)
		})
	}
	var nilFlag *flag.InternalFlag
	assert.Nil(t, nilFlag.DeepCopy())
}

func TestInternalFlag_DeepCopyFullFlag(t *testing.T) {
	t.Parallel()
	str := func(s string) *string { return &s }
	flt := func(f float64) *float64 { return &f }
	boolean := func(b bool) *bool { return &b }
	date := time.Date(2021, 1, 2, 3, 4, 5, 0, time.UTC)
	build := func() *flag.InternalFlag {
		step := &flag.ProgressiveRolloutStep{Variation: str("A"), Percentage: flt(1), Date: &date}
		d := date

		return &flag.InternalFlag{
			BucketingKey: str("k"), TrackEvents: boolean(true), Disable: boolean(false), Version: str("1"),
			Rules: &[]flag.Rule{{
				Name: str("r"), Query: str("q"), VariationResult: str("A"), Disable: boolean(false),
				Percentages:        &map[string]float64{"A": 1},
				ProgressiveRollout: &flag.ProgressiveRollout{Initial: step, End: step},
			}},
			DefaultRule:     &flag.Rule{Percentages: &map[string]float64{"A": 1}},
			Experimentation: &flag.ExperimentationRollout{Start: &d, End: &d},
			Scheduled: &[]flag.ScheduledStep{{
				Version: str("2"), Metadata: &map[string]any{"a": []any{"b"}},
				Date: &d,
			}},
		}
	}
	orig := build()
	cp := orig.DeepCopy()
	assert.Equal(t, orig, cp)

	(*cp.Rules)[0].ProgressiveRollout.End.Percentage = flt(9)
	*(*cp.Rules)[0].ProgressiveRollout.Initial.Variation = "changed"
	*(*cp.Rules)[0].Name = "changed"
	(*(*cp.Rules)[0].Percentages)["A"] = 9
	*cp.DefaultRule.Percentages = map[string]float64{}
	*cp.Experimentation.Start = time.Time{}
	*(*cp.Scheduled)[0].Date = time.Time{}
	(*(*cp.Scheduled)[0].Metadata)["a"].([]any)[0] = "changed"
	*cp.Version = "changed"
	assert.Equal(t, build(), orig)
}
