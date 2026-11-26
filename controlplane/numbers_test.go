package controlplane_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/modules/core/evaluation"
	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

const hugeUnsigned uint64 = 12345678901234567890

func numbersFlags() controlplane.Flags {
	return controlplane.Flags{"n": {
		Variations: map[string]any{
			keyFloat:   2.0,
			keyInt:     2,
			"negative": -5,
			"big":      9007199254740993,
			keyHuge:    hugeUnsigned,
			"exp":      1.5e30,
			"fraction": 0.1,
			"zero":     0.0,
		},
		DefaultRule: &controlplane.Rule{Variation: new(keyFloat)},
	}}
}

func TestNumberTypesSurviveBothFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		marshal    func(controlplane.Flags) ([]byte, error)
		name       string
		format     string
		wantSpells []string
	}{
		{
			controlplane.MarshalFlags,
			"as yaml",
			controlplane.FormatYAML,
			[]string{
				"float: 2.0\n",
				"int: 2\n",
				"big: 9007199254740993\n",
				"huge: 12345678901234567890\n",
				"zero: 0.0\n",
			},
		},
		{
			controlplane.MarshalFlagsJSON,
			"as json",
			controlplane.FormatJSON,
			[]string{
				`"float": 2.0,`,
				`"int": 2,`,
				`"big": 9007199254740993,`,
				`"huge": 12345678901234567890,`,
				`"zero": 0.0`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := tt.marshal(numbersFlags())
			require.NoError(t, err)

			for _, spelled := range tt.wantSpells {
				assert.Contains(t, string(data), spelled)
			}

			got, info, err := controlplane.UnmarshalFlags(data, tt.format)
			require.NoError(t, err)
			assert.Empty(t, info.UnknownFields)

			variations := got["n"].Variations
			assert.IsType(t, float64(0), variations[keyFloat])
			assert.InDelta(t, 2.0, variations[keyFloat], 0)
			assert.Equal(t, 2, variations[keyInt])
			assert.Equal(t, 9007199254740993, variations["big"])
			assert.Equal(t, hugeUnsigned, variations[keyHuge])
			assert.IsType(t, float64(0), variations["exp"])
			assert.InEpsilon(t, 1.5e30, variations["exp"], 0)
			assert.Equal(t, numbersFlags(), got)
		})
	}
}

func TestNumberOutOfRangeLiterals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		data   string
		format string
	}{
		{
			"yaml integer above uint64",
			"f:\n  metadata:\n    k: 123456789012345678901234567890\n",
			controlplane.FormatYAML,
		},
		{
			"json integer above uint64",
			`{"f":{"metadata":{"k":123456789012345678901234567890}}}`,
			controlplane.FormatJSON,
		},
		{"json float overflow", `{"f":{"metadata":{"k":1e999}}}`, controlplane.FormatJSON},
		{"yaml nan", "f:\n  metadata:\n    k: .nan\n", controlplane.FormatYAML},
		{"yaml infinity", "f:\n  metadata:\n    k: -.inf\n", controlplane.FormatYAML},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := controlplane.UnmarshalFlags([]byte(tt.data), tt.format)
			require.ErrorIs(t, err, controlplane.ErrDecode)
		})
	}
}

// evaluateWithEngine loads flag bytes with the engine's loader and evaluates the flag "n" for float64 and int.
func evaluateWithEngine(t *testing.T, data []byte, format string) (string, string) {
	t.Helper()

	dtos, err := cache.ConvertToFlagStruct(data, format)
	require.NoError(t, err)

	flag := dtos["n"]
	internal := flag.Convert()
	svc := flagdef.NewService()

	require.NoError(t, internal.IsValid(svc))

	ctx := flagdef.Context{Service: svc}

	floatRes, floatErr := evaluation.Evaluate[float64](
		&internal,
		"n",
		ffcontext.NewEvaluationContext("u"),
		ctx,
		"float64",
		-1,
	)
	intRes, intErr := evaluation.Evaluate[int](&internal, "n", ffcontext.NewEvaluationContext("u"), ctx, "int", -1)

	return describe(floatRes.Value, floatRes.ErrorCode, floatErr), describe(intRes.Value, intRes.ErrorCode, intErr)
}

func describe(value any, code flagdef.ErrorCode, err error) string {
	if err != nil {
		return "error " + code
	}

	return fmt.Sprintf("ok %v", value)
}

// TestEngineSeesTheSameNumbersAfterARoundTrip: Float64Variation on a 2.0 variation and IntVariation on a 2
// variation give the same result before and after a round trip through this package, in YAML and in JSON.
func TestEngineSeesTheSameNumbersAfterARoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		source    string
		format    string
		wantFloat string
		wantInt   string
	}{
		{
			"yaml float variation",
			"n:\n  variations:\n    a: 2.0\n    b: 3.5\n  defaultRule:\n    variation: a\n",
			controlplane.FormatYAML,
			resultOK,
			resultOK,
		},
		{
			"yaml int variation",
			"n:\n  variations:\n    a: 2\n    b: 3\n  defaultRule:\n    variation: a\n",
			controlplane.FormatYAML,
			"error TYPE_MISMATCH",
			resultOK,
		},
		{
			"json float variation",
			`{"n":{"variations":{"a":2.0,"b":3.5},"defaultRule":{"variation":"a"}}}`,
			controlplane.FormatJSON,
			resultOK,
			resultOK,
		},
		{
			"json int variation",
			`{"n":{"variations":{"a":2,"b":3},"defaultRule":{"variation":"a"}}}`,
			controlplane.FormatJSON,
			resultOK,
			resultOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			beforeFloat, beforeInt := evaluateWithEngine(t, []byte(tt.source), tt.format)
			assert.Equal(t, tt.wantFloat, beforeFloat)
			assert.Equal(t, tt.wantInt, beforeInt)

			flags, _, err := controlplane.UnmarshalFlags([]byte(tt.source), tt.format)
			require.NoError(t, err)

			for _, asJSON := range []bool{false, true} {
				marshal, format := controlplane.MarshalFlags, controlplane.FormatYAML
				if asJSON {
					marshal, format = controlplane.MarshalFlagsJSON, controlplane.FormatJSON
				}

				data, marshalErr := marshal(flags)
				require.NoError(t, marshalErr)

				afterFloat, afterInt := evaluateWithEngine(t, data, format)

				// A YAML source written as JSON loses the int/float spelling for the engine (it reads every JSON
				// number as a float), so only same-format writing must match exactly.
				if format == tt.format {
					assert.Equal(t, beforeFloat, afterFloat, "float64 evaluation of %s", data)
					assert.Equal(t, beforeInt, afterInt, "int evaluation of %s", data)
				} else {
					assert.Equal(t, resultOK, afterInt)
				}
			}
		})
	}
}
