package flag_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/stretchr/testify/assert"
)

func TestInternalFlag_DeepCopy(t *testing.T) {
	t.Parallel()
	ptr := func(v any) *any { return &v }
	tests := []struct {
		name  string
		value func() any
		// mutate changes the copied value in place.
		mutate func(v any)
	}{
		{name: "nil", value: func() any { return nil }, mutate: func(any) {}},
		{name: "scalar", value: func() any { return "a" }, mutate: func(any) {}},
		{name: "map of any", value: func() any { return map[string]any{"a": []any{1}} }, mutate: func(v any) {
			v.(map[string]any)["a"].([]any)[0] = 2
		}},
		{name: "typed slice", value: func() any { return []string{"a"} }, mutate: func(v any) { v.([]string)[0] = "b" }},
		{name: "typed map", value: func() any { return map[string]string{"a": "a"} }, mutate: func(v any) { v.(map[string]string)["a"] = "b" }},
		{name: "typed map of slices", value: func() any { return map[string][]int{"a": {1}} }, mutate: func(v any) {
			v.(map[string][]int)["a"][0] = 2
		}},
		{name: "nil map and slice", value: func() any { return map[string]any{"m": map[string]string(nil), "s": []int(nil), "n": nil} },
			mutate: func(any) {}},
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
			assert.Equal(t, build(), orig, "changing the copy must not change the original")
		})
	}
	var nilFlag *flag.InternalFlag
	assert.Nil(t, nilFlag.DeepCopy())
}
