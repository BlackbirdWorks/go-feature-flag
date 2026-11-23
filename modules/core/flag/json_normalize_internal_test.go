package flag

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type normalizeStruct struct {
	Name string `json:"name"`
}

// normalizeJSONValue must give exactly what marshalling then unmarshalling gives.
func TestNormalizeJSONValue_MatchesTheJSONRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		in        map[string]any
		wantKnown bool
	}{
		{
			name: "strings, bools, nil and floats",
			in:   map[string]any{"a": "x", "b": true, "c": nil, "d": 1.5, "e": ""}, wantKnown: true,
		},
		{
			name: "all the integer kinds become float64",
			in: map[string]any{
				"i": 1, "i8": int8(-2), "i16": int16(3), "i32": int32(4), "i64": int64(1 << 60),
				"u": uint(5), "u8": uint8(6), "u16": uint16(7), "u32": uint32(8), "u64": uint64(math.MaxUint64),
			}, wantKnown: true,
		},
		{
			name: "nested maps and slices",
			in: map[string]any{
				"m": map[string]any{"k": []any{1, "two", 3.5, map[string]any{"z": nil}}},
				"s": []any{},
			}, wantKnown: true,
		},
		{name: "float32 is left to the encoding", in: map[string]any{"f": float32(0.1)}, wantKnown: false},
		{name: "struct is left to the encoding", in: map[string]any{"s": normalizeStruct{Name: "n"}}, wantKnown: false},
		{name: "time is left to the encoding", in: map[string]any{"t": time.Unix(0, 0).UTC()}, wantKnown: false},
		{name: "typed slice is left to the encoding", in: map[string]any{"s": []string{"a"}}, wantKnown: false},
		{name: "NaN is left to the encoding", in: map[string]any{"n": math.NaN()}, wantKnown: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := normalizeJSONValue(tt.in)
			assert.Equal(t, tt.wantKnown, ok)
			if !ok {
				return
			}
			raw, err := json.Marshal(tt.in)
			require.NoError(t, err)
			var want any
			require.NoError(t, json.Unmarshal(raw, &want))
			assert.Equal(t, want, got)
		})
	}
}
