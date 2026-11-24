package flag

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/utils"
)

type normalizeStruct struct {
	Name string `json:"name"`
}

type marshalerValue struct{ n int }

func (m marshalerValue) MarshalJSON() ([]byte, error) { return json.Marshal(map[string]int{"n": m.n}) }

type unmarshalableValue struct{}

func (unmarshalableValue) MarshalJSON() ([]byte, error) { return nil, assert.AnError }

type namedMap map[string]any

// The data JsonLogic reads must be what the JSON round trip of the context gives (what was done
// before the fast path), including when that round trip fails.
func TestQueryContext_DataOnce_MatchesTheJSONRoundTrip(t *testing.T) {
	t.Parallel()
	cyclicMap := map[string]any{}
	cyclicMap["self"] = cyclicMap
	cyclicSlice := []any{nil}
	cyclicSlice[0] = cyclicSlice
	var nilPtr *normalizeStruct
	deep := any("leaf")
	for range 5000 { // deeper than the fast path goes, but not a cycle
		deep = []any{deep}
	}

	tests := []struct {
		name   string
		custom map[string]any
		// wantErr is whether the JSON round trip, and so the fast path, must fail for these attributes
		wantErr bool
	}{
		{name: "nil map", custom: map[string]any{"p": map[string]any(nil)}},
		{name: "nil slice", custom: map[string]any{"p": []any(nil)}},
		{name: "empty map and slice", custom: map[string]any{"m": map[string]any{}, "s": []any{}}},
		{name: "nil map and slice nested", custom: map[string]any{"p": map[string]any{
			"m": map[string]any(nil), "s": []any{[]any(nil), map[string]any(nil)}}}},
		{name: "nil typed slices", custom: map[string]any{"s": []string(nil), "i": []int(nil), "b": []byte(nil)}},
		{name: "typed nil pointer", custom: map[string]any{"p": nilPtr}},
		{name: "nested structs", custom: map[string]any{"s": struct {
			A normalizeStruct
			B *normalizeStruct
			C []normalizeStruct
		}{C: []normalizeStruct{{Name: "x"}}}}},
		{
			name:   "json.Number",
			custom: map[string]any{"n": json.Number("12.50"), "l": json.Number("123456789012345678901")},
		},
		{name: "time.Time", custom: map[string]any{"t": time.Date(2024, 1, 2, 3, 4, 5, 6, time.UTC)}},
		{name: "[]byte", custom: map[string]any{"b": []byte("hello")}},
		{name: "custom MarshalJSON", custom: map[string]any{"m": marshalerValue{n: 3}}},
		{name: "MarshalJSON error", wantErr: true, custom: map[string]any{"m": unmarshalableValue{}}},
		{name: "NaN", wantErr: true, custom: map[string]any{"f": math.NaN()}},
		{name: "+Inf", wantErr: true, custom: map[string]any{"f": math.Inf(1)}},
		{name: "-Inf nested", wantErr: true, custom: map[string]any{"f": []any{math.Inf(-1)}}},
		{name: "float32", custom: map[string]any{"f": float32(0.1), "g": float32(16777217)}},
		{
			name:   "int64 above 2^53",
			custom: map[string]any{"i": int64(1<<53 + 1), "j": int64(math.MaxInt64), "k": int64(math.MinInt64)},
		},
		{name: "uint64", custom: map[string]any{"u": uint64(math.MaxUint64), "v": uint64(1<<53 + 1)}},
		{name: "negative zero and extremes", custom: map[string]any{
			"z": math.Copysign(0, -1), "max": math.MaxFloat64, "min": math.SmallestNonzeroFloat64}},
		{name: "map with int keys", custom: map[string]any{"m": map[int]string{2: "b", 1: "a"}}},
		{name: "map with non string keys that can not be encoded", custom: map[string]any{"m": map[any]any{"a": 1}}},
		{name: "map[string]string", custom: map[string]any{"m": map[string]string{"a": "b"}}},
		{name: "named map type", custom: map[string]any{"m": namedMap{"a": 1}}},
		{name: "invalid UTF-8 in a string", custom: map[string]any{"s": "a\xffb"}},
		{name: "invalid UTF-8 in a key", custom: map[string]any{"m": map[string]any{"a\xffb": 1}}},
		{name: "unicode and html characters", custom: map[string]any{"s": "é <>&\"\\ \x00"}},
		{name: "channel", wantErr: true, custom: map[string]any{"c": make(chan int)}},
		{name: "cyclic map", wantErr: true, custom: map[string]any{"c": cyclicMap}},
		{name: "cyclic slice", wantErr: true, custom: map[string]any{"c": cyclicSlice}},
		{name: "very deep slice", custom: map[string]any{"d": deep}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := ffcontext.NewEvaluationContextBuilder("user").Build()
			for k, v := range tt.custom {
				ctx.AddCustomAttribute(k, v)
			}
			qc := &queryContext{svc: NewService(), ctx: ctx}
			defer qc.release()
			got, gotErr := qc.dataOnce()

			var want any
			raw, wantErr := json.Marshal(utils.ContextToMap(ctx))
			if wantErr == nil {
				wantErr = json.Unmarshal(raw, &want)
			}
			assert.Equal(t, tt.wantErr, wantErr != nil, "the round trip of the attributes")
			if wantErr != nil {
				assert.Error(t, gotErr, "the round trip fails, so must the fast path")

				return
			}
			require.NoError(t, gotErr)
			assert.Equal(t, want, got)
		})
	}
}
