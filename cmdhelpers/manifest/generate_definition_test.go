package manifest_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmdhelpers/manifest"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	emptyJSON                   = "empty.json"
	half                        = "Half"
	invalidConfigurationForFlag = "invalid configuration for flag bad-flag: impossible to find type"
	one                         = "One"
)

// captureHandler stores every slog record so tests can assert message content
// and severity emitted by GenerateDefinition. The handler is wired in as the
// logger passed with manifest.WithLogger.
type captureHandler struct {
	msgs   []string
	levels []slog.Level
}

func (*captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	msg := r.Message
	r.Attrs(func(a slog.Attr) bool {
		msg += " " + a.Key + "=" + a.Value.String()

		return true
	})

	h.msgs = append(h.msgs, msg)
	h.levels = append(h.levels, r.Level)

	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

// newCaptureLogger returns a logger backed by a captureHandler (one per test case, so the
// cases can run in parallel) and the handler to inspect.
func newCaptureLogger() (*slog.Logger, *captureHandler) {
	h := &captureHandler{}

	return slog.New(h), h
}

// variationsOf builds the map[string]*any value expected by InternalFlag.Variations
// from a plain map[string]any literal.
func variationsOf(values map[string]any) map[string]*any {
	out := make(map[string]*any, len(values))
	for k, v := range values {
		out[k] = &v
	}

	return out
}

func defaultRule(variation string) *flag.Rule {
	return &flag.Rule{
		Name:            new("defaultRule"),
		VariationResult: new(variation),
	}
}

// boolFlag returns a minimal boolean InternalFlag whose variations are True/False
// and whose default rule selects the variation passed in.
func boolFlag(defaultVariation string, metadata map[string]any) flag.InternalFlag {
	internalFlag := flag.InternalFlag{
		Variations:  new(variationsOf(map[string]any{fixtures.VariationTrue: true, fixtures.VariationFalse: false})),
		DefaultRule: defaultRule(defaultVariation),
	}
	if metadata != nil {
		internalFlag.Metadata = &metadata
	}

	return internalFlag
}

// boolFlagWithExperimentation is like boolFlag but attaches an experimentation window.
func boolFlagWithExperimentation(
	defaultVariation string,
	metadata map[string]any,
	experimentationStart, experimentationEnd time.Time,
) flag.InternalFlag {
	internalFlag := boolFlag(defaultVariation, metadata)
	s, e := experimentationStart, experimentationEnd
	internalFlag.Experimentation = &flag.ExperimentationRollout{
		Start: &s,
		End:   &e,
	}

	return internalFlag
}

func readGolden(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", "generate_definition", name))
	require.NoError(t, err)

	return string(data)
}

type generateDefinitionCase struct {
	flags      map[string]flag.InternalFlag
	name       string
	goldenFile string
	// wantErr, when non-empty, asserts the returned error contains it.
	// In that case the manifest output (and golden file) are not checked.
	wantErr string
	// wantLogs is the set of expected slog messages. Map iteration is
	// non-deterministic, so order is not asserted.
	wantLogs []string
}

func runGenerateDefinitionTests(t *testing.T, tests []generateDefinitionCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, handler := newCaptureLogger()

			got, err := manifest.GenerateDefinitionFromInternalFlags(tt.flags, manifest.WithLogger(logger))

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)

			actual, mErr := json.MarshalIndent(got, "", "  ")
			require.NoError(t, mErr)
			expected := readGolden(t, tt.goldenFile)
			assert.Equal(t, expected, string(actual))

			assert.ElementsMatch(t, tt.wantLogs, handler.msgs)

			for _, lvl := range handler.levels {
				assert.Equal(t, slog.LevelWarn, lvl)
			}
		})
	}
}

func TestGenerateDefinition(t *testing.T) {
	t.Parallel()

	runGenerateDefinitionTests(t, []generateDefinitionCase{
		{
			name:       "empty input produces empty manifest",
			flags:      map[string]flag.InternalFlag{},
			goldenFile: emptyJSON,
		},
		{
			name: "boolean flag",
			flags: map[string]flag.InternalFlag{
				"boolean-flag": boolFlag(fixtures.VariationFalse, map[string]any{
					fixtures.DefaultValue:     false,
					fixtures.DescriptionField: "a boolean flag",
				}),
			},
			goldenFile: "boolean.json",
		},
		{
			name: "boolean flag with experimentation",
			flags: map[string]flag.InternalFlag{
				"exp-flag": boolFlagWithExperimentation(
					fixtures.VariationFalse,
					map[string]any{
						fixtures.DefaultValue:     false,
						fixtures.DescriptionField: "A/B experiment",
					},
					time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
					time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
				),
			},
			goldenFile: "experimentation.json",
		},
		{
			name: "string flag",
			flags: map[string]flag.InternalFlag{
				"string-flag": {
					Variations:  new(variationsOf(map[string]any{"A": "a", "B": "b"})),
					DefaultRule: defaultRule("A"),
					Metadata: new(map[string]any{
						fixtures.DefaultValue:     "a",
						fixtures.DescriptionField: "strings",
					}),
				},
			},
			goldenFile: "string.json",
		},
		{
			name: "integer flag",
			flags: map[string]flag.InternalFlag{
				"int-flag": {
					Variations:  new(variationsOf(map[string]any{one: 1, "Two": 2})),
					DefaultRule: defaultRule(one),
					Metadata: new(map[string]any{
						fixtures.DefaultValue:     1,
						fixtures.DescriptionField: "ints",
					}),
				},
			},
			goldenFile: "integer.json",
		},
		{
			name: "float flag without description",
			flags: map[string]flag.InternalFlag{
				"float-flag": {
					Variations:  new(variationsOf(map[string]any{half: 0.5, "OneAndHalf": 1.5})),
					DefaultRule: defaultRule(half),
					Metadata: new(map[string]any{
						fixtures.DefaultValue: 0.5,
					}),
				},
			},
			goldenFile: "float.json",
		},
		{
			name: "object flag",
			flags: map[string]flag.InternalFlag{
				"object-flag": {
					Variations: new(variationsOf(map[string]any{
						"A": map[string]any{fixtures.MetadataKey: "value-a"},
						"B": map[string]any{fixtures.MetadataKey: "value-b"},
					})),
					DefaultRule: defaultRule("A"),
					Metadata: new(map[string]any{
						fixtures.DefaultValue:     map[string]any{fixtures.MetadataKey: "value-a"},
						fixtures.DescriptionField: "objects",
					}),
				},
			},
			goldenFile: "object.json",
		},
		{
			name: "int and float .0 mix resolves to integer",
			flags: map[string]flag.InternalFlag{
				"mixed-flag": {
					Variations:  new(variationsOf(map[string]any{"Whole": 1, half: 2.0})),
					DefaultRule: defaultRule("Whole"),
					Metadata: new(map[string]any{
						fixtures.DefaultValue:     1,
						fixtures.DescriptionField: "int as float",
					}),
				},
			},
			goldenFile: "int_as_float.json",
		},
		{
			name: "int and non-whole float mix resolves to float",
			flags: map[string]flag.InternalFlag{
				"mixed-flag": {
					Variations:  new(variationsOf(map[string]any{one: 1, "OneAndHalf": 1.5})),
					DefaultRule: defaultRule(one),
					Metadata: new(map[string]any{
						fixtures.DefaultValue:     1,
						fixtures.DescriptionField: "float and int",
					}),
				},
			},
			goldenFile: "float_and_int.json",
		},
		{
			name: "missing description metadata defaults to empty string",
			flags: map[string]flag.InternalFlag{
				"minimal-flag": boolFlag(fixtures.VariationFalse, map[string]any{
					fixtures.DefaultValue: false,
				}),
			},
			goldenFile: "no_description.json",
		},
		{
			name: "non-string description is ignored",
			flags: map[string]flag.InternalFlag{
				"weird-flag": boolFlag(fixtures.VariationFalse, map[string]any{
					fixtures.DefaultValue:     false,
					fixtures.DescriptionField: 42,
				}),
			},
			goldenFile: "description_non_string.json",
		},
	})
}

func TestGenerateDefinition_SkipsAndErrors(t *testing.T) {
	t.Parallel()

	runGenerateDefinitionTests(t, []generateDefinitionCase{
		{
			name: "flag without metadata is skipped and logged",
			flags: map[string]flag.InternalFlag{
				"no-meta": {
					Variations: new(variationsOf(
						map[string]any{fixtures.VariationTrue: true, fixtures.VariationFalse: false},
					)),
					DefaultRule: defaultRule(fixtures.VariationFalse),
				},
			},
			goldenFile: emptyJSON,
			wantLogs:   []string{"flag ignored: no default value provided flag=no-meta"},
		},
		{
			name: "metadata without defaultValue is skipped and logged",
			flags: map[string]flag.InternalFlag{
				"no-default": boolFlag(fixtures.VariationFalse, map[string]any{
					fixtures.DescriptionField: "no default",
				}),
			},
			goldenFile: emptyJSON,
			wantLogs:   []string{"flag ignored: no default value provided flag=no-default"},
		},
		{
			name: "valid flag is kept while invalid sibling is skipped",
			flags: map[string]flag.InternalFlag{
				"valid-flag": boolFlag(fixtures.VariationTrue, map[string]any{
					fixtures.DefaultValue: true,
				}),
				"skipped-flag": boolFlag(fixtures.VariationTrue, map[string]any{
					fixtures.DescriptionField: "no default",
				}),
			},
			goldenFile: "partial_skip.json",
			wantLogs:   []string{"flag ignored: no default value provided flag=skipped-flag"},
		},
		{
			name: "multiple valid flags are all serialized",
			flags: map[string]flag.InternalFlag{
				"flag-a": boolFlag(fixtures.VariationTrue, map[string]any{
					fixtures.DefaultValue: true,
				}),
				"flag-b": {
					Variations:  new(variationsOf(map[string]any{one: 1, "Two": 2})),
					DefaultRule: defaultRule(one),
					Metadata: new(map[string]any{
						fixtures.DefaultValue:     1,
						fixtures.DescriptionField: "second",
					}),
				},
			},
			goldenFile: "multi_flags.json",
		},
		{
			name: "unsupported variation type returns wrapped error",
			flags: map[string]flag.InternalFlag{
				fixtures.FlagKeyBad: {
					Variations: new(variationsOf(map[string]any{
						"Slice": []int{1, 2, 3},
					})),
					DefaultRule: defaultRule("Slice"),
					Metadata: new(map[string]any{
						fixtures.DefaultValue: []int{1, 2, 3},
					}),
				},
			},
			wantErr: invalidConfigurationForFlag,
		},
		{
			name: "incompatible variation types return wrapped error",
			flags: map[string]flag.InternalFlag{
				fixtures.FlagKeyBad: {
					Variations:  new(variationsOf(map[string]any{"A": true, "B": "string"})),
					DefaultRule: defaultRule("A"),
					Metadata: new(map[string]any{
						fixtures.DefaultValue: true,
					}),
				},
			},
			wantErr: invalidConfigurationForFlag,
		},
		{
			name: "empty variations map returns wrapped error",
			flags: map[string]flag.InternalFlag{
				fixtures.FlagKeyBad: {
					Variations:  new(variationsOf(map[string]any{})),
					DefaultRule: defaultRule(""),
					Metadata: new(map[string]any{
						fixtures.DefaultValue: "ignored",
					}),
				},
			},
			wantErr: invalidConfigurationForFlag,
		},
	})
}
