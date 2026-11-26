package controlplane_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/modules/core/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

func anyPtr(v any) *any { return new(v) }

func at(day int) *time.Time { return fixtureTime(day) }

// TestEngineEquivalence marshals flags with this package, loads them with the engine's loader and compares with
// the engine's own structs, field by field and after conversion.
func TestEngineEquivalence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		flags controlplane.Flags
		want  map[string]dto.DTO
		name  string
	}{
		{
			name: "plain flag with every scalar",
			flags: controlplane.Flags{"f": {
				TrackEvents: new(false), Disable: new(true), Version: new("v2"), BucketingKey: new("user"),
				Variations:  map[string]any{"on": true, variationOff: false},
				DefaultRule: &controlplane.Rule{Variation: new(variationOff)},
				Metadata:    map[string]any{keyOwner: "me", "n": 3, "nested": map[string]any{"k": textYes}},
			}},
			want: map[string]dto.DTO{"f": {
				TrackEvents: new(false), Disable: new(true), Version: new("v2"), BucketingKey: new("user"),
				Variations:  &map[string]*any{"on": anyPtr(true), variationOff: anyPtr(false)},
				DefaultRule: &flagdef.Rule{VariationResult: new(variationOff)},
				Metadata:    &map[string]any{keyOwner: "me", "n": 3, "nested": map[string]any{"k": textYes}},
			}},
		},
		{
			name: "arbitrary variation values and targeting",
			flags: controlplane.Flags{"g": {
				Variations: map[string]any{
					"s": "007",
					"i": 7,
					"f": 1.5,
					"m": map[string]any{"a": "on"},
					"l": []any{"y", 1},
					"z": nil,
				},
				Targeting: []controlplane.Rule{
					{Name: new("r1"), Query: new(`key eq "a"`), Variation: new("s")},
					{
						Name:       new("r2"),
						Query:      new(queryBeta),
						Percentage: map[string]float64{"s": 20.5, "i": 79.5},
						Disable:    new(true),
					},
				},
				DefaultRule: &controlplane.Rule{Percentage: map[string]float64{"s": 0, "i": 100}},
			}},
			want: map[string]dto.DTO{"g": {
				Variations: &map[string]*any{
					"s": anyPtr("007"),
					"i": anyPtr(7),
					"f": anyPtr(1.5),
					"m": anyPtr(map[string]any{"a": "on"}),
					"l": anyPtr([]any{"y", 1}),
					"z": nil,
				},
				Rules: &[]flagdef.Rule{
					{Name: new("r1"), Query: new(`key eq "a"`), VariationResult: new("s")},
					{
						Name:        new("r2"),
						Query:       new(queryBeta),
						Percentages: &map[string]float64{"s": 20.5, "i": 79.5},
						Disable:     new(true),
					},
				},
				DefaultRule: &flagdef.Rule{Percentages: &map[string]float64{"s": 0, "i": 100}},
			}},
		},
		{
			name: "rollouts",
			flags: controlplane.Flags{"h": {
				Variations: map[string]any{"a": "a", "b": "b"},
				Targeting: []controlplane.Rule{
					{Name: new("ramp"), Query: new(queryXEq1), ProgressiveRollout: &controlplane.ProgressiveRollout{
						Initial: &controlplane.ProgressiveRolloutStep{
							Variation:  new("a"),
							Percentage: new(0.0),
							Date:       at(1),
						},
						End: &controlplane.ProgressiveRolloutStep{
							Variation:  new("b"),
							Percentage: new(100.0),
							Date:       at(2),
						},
					}},
				},
				DefaultRule: &controlplane.Rule{Variation: new("a")},
				ScheduledRollout: []controlplane.ScheduledStep{{
					Date: at(3), Disable: new(true), Version: new("v3"), BucketingKey: new("k"),
					Variations:      map[string]any{"a": 1},
					Targeting:       []controlplane.Rule{{Name: new("r"), Variation: new("a")}},
					DefaultRule:     &controlplane.Rule{Variation: new("b")},
					Experimentation: &controlplane.Experimentation{Start: at(4)},
					Metadata:        map[string]any{"m": 1},
					TrackEvents:     new(true),
				}},
				Experimentation: &controlplane.Experimentation{Start: at(5), End: at(6)},
			}},
			want: map[string]dto.DTO{"h": {
				Variations: &map[string]*any{"a": anyPtr("a"), "b": anyPtr("b")},
				Rules: &[]flagdef.Rule{
					{Name: new("ramp"), Query: new(queryXEq1), ProgressiveRollout: &flagdef.ProgressiveRollout{
						Initial: &flagdef.ProgressiveRolloutStep{
							Variation:  new("a"),
							Percentage: new(0.0),
							Date:       at(1),
						},
						End: &flagdef.ProgressiveRolloutStep{
							Variation:  new("b"),
							Percentage: new(100.0),
							Date:       at(2),
						},
					}},
				},
				DefaultRule:     &flagdef.Rule{VariationResult: new("a")},
				Scheduled:       &[]flagdef.ScheduledStep{wantScheduledStep()},
				Experimentation: &dto.ExperimentationDto{Start: at(5), End: at(6)},
			}},
		},
		{
			name: "explicit empty values stay distinct from absent ones",
			flags: controlplane.Flags{"e": {
				Version: new(""), BucketingKey: new(""), Converter: new(""),
				Variations: map[string]any{"": "x", "y": "z"},
				Targeting: []controlplane.Rule{
					{Name: new(""), Query: new(""), Variation: new(""), Percentage: map[string]float64{}},
				},
				DefaultRule:      &controlplane.Rule{Variation: new("")},
				Metadata:         map[string]any{},
				ScheduledRollout: []controlplane.ScheduledStep{},
			}, "absent": {Variations: map[string]any{"a": 1}}},
			want: map[string]dto.DTO{"e": {
				Version: new(""), BucketingKey: new(""), Converter: new(""),
				Variations: &map[string]*any{"": anyPtr("x"), "y": anyPtr("z")},
				Rules: &[]flagdef.Rule{
					{Name: new(""), Query: new(""), VariationResult: new(""), Percentages: &map[string]float64{}},
				},
				DefaultRule: &flagdef.Rule{VariationResult: new("")},
				Metadata:    &map[string]any{},
				Scheduled:   &[]flagdef.ScheduledStep{},
			}, "absent": {Variations: &map[string]*any{"a": anyPtr(1)}}},
		},
		{
			name:  "converter",
			flags: controlplane.Flags{"k": {Converter: new("kayenta")}},
			want:  map[string]dto.DTO{"k": {Converter: new("kayenta")}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b, err := controlplane.MarshalFlags(tt.flags)
			require.NoError(t, err)

			got, err := cache.ConvertToFlagStruct(b, controlplane.FormatYAML)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)

			for name := range got {
				want, have := tt.want[name], got[name]
				assert.Equal(t, want.Convert(), have.Convert(), "converted flag %s", name)
			}

			// The JSON form loads the same flags: compare with the engine's own JSON form of the expected flags.
			j, err := controlplane.MarshalFlagsJSON(tt.flags)
			require.NoError(t, err)

			viaJSON, err := cache.ConvertToFlagStruct(j, controlplane.FormatJSON)
			require.NoError(t, err)

			wantJSON, err := json.Marshal(tt.want)
			require.NoError(t, err)

			wantViaJSON, err := cache.ConvertToFlagStruct(wantJSON, controlplane.FormatJSON)
			require.NoError(t, err)
			assert.Equal(t, wantViaJSON, viaJSON)
		})
	}
}

// TestEngineFlagFilesRoundTrip loads every flag file under the repository testdata with the engine, passes it
// through this package and loads the result again: the engine sees the same flags and this package knows every
// field of the file.
func TestEngineFlagFilesRoundTrip(t *testing.T) {
	t.Parallel()

	var files []string

	root := filepath.Join("..", "testdata")
	require.NoError(t, filepath.WalkDir(root, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		switch filepath.Ext(path) {
		case extYAML, ".yml", extJSON, ".toml":
			files = append(files, path)
		}

		return nil
	}))
	require.NotEmpty(t, files)

	rejected := map[string]bool{}

	for _, path := range files {
		data, err := os.ReadFile(path)
		require.NoError(t, err)

		format := strings.TrimPrefix(filepath.Ext(path), ".")
		name := filepath.ToSlash(strings.TrimPrefix(path, root))

		engine, err := cache.ConvertToFlagStruct(data, format)
		if err != nil {
			rejected[name] = true

			continue
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			asYAML, yamlErr := yaml.Marshal(engine)
			require.NoError(t, yamlErr)

			flags, info, decodeErr := controlplane.UnmarshalFlags(asYAML, controlplane.FormatYAML)
			require.NoError(t, decodeErr)
			assert.Empty(t, info.UnknownFields, "the engine file has fields this package does not model")

			out, marshalErr := controlplane.MarshalFlags(flags)
			require.NoError(t, marshalErr)

			again, convertErr := cache.ConvertToFlagStruct(out, controlplane.FormatYAML)
			require.NoError(t, convertErr)
			assert.Equal(t, engine, again)

			outJSON, jsonErr := controlplane.MarshalFlagsJSON(flags)
			require.NoError(t, jsonErr)

			viaJSON, convertJSONErr := cache.ConvertToFlagStruct(outJSON, controlplane.FormatJSON)
			require.NoError(t, convertJSONErr)
			assert.Len(t, viaJSON, len(engine))
		})
	}

	// The files that the engine's own loader rejects (flag state dumps, files that are invalid on purpose).
	assert.Equal(t, map[string]bool{
		"/ffclient/all_flags/marshal_json/error_in_flag_0.json":        true,
		"/ffclient/all_flags/marshal_json/module_not_init.json":        true,
		"/ffclient/all_flags/marshal_json/offline.json":                true,
		"/ffclient/all_flags/marshal_json/valid_multiple_types.json":   true,
		"/ffclient/get_flagstates/marshal_json/all_flags.json":         true,
		"/ffclient/get_flagstates/marshal_json/valid_flag1_flag4.json": true,
		"/internal/dto/rollout.json":                                   true,
		"/internal/dto/rollout.toml":                                   true,
		"/internal/dto/rollout.yaml":                                   true,
		"/invalid-flag-config.json":                                    true,
	}, rejected)
}

// yamlNames returns the yaml field names of a struct, flattening inline embedded structs and skipping "-".
func yamlNames(typ reflect.Type) map[string]bool {
	names := map[string]bool{}

	for field := range typ.Fields() {
		if field.Anonymous {
			for name := range yamlNames(field.Type) {
				names[name] = true
			}

			continue
		}

		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name != "" && name != "-" {
			names[name] = true
		}
	}

	return names
}

// TestEngineFieldParity fails when the engine gains a field that this package does not model (or the package
// grows a field the engine does not read). exclude lists the fields that are left out on purpose.
func TestEngineFieldParity(t *testing.T) {
	t.Parallel()

	tests := []struct { //nolint:govet // fieldalignment: the columns of the table are in reading order
		name    string
		engine  reflect.Type
		ours    reflect.Type
		exclude map[string]string
	}{
		{"flag", reflect.TypeFor[dto.DTO](), reflect.TypeFor[controlplane.Flag](), nil},
		{"rule", reflect.TypeFor[flagdef.Rule](), reflect.TypeFor[controlplane.Rule](), nil},
		{
			"progressive rollout",
			reflect.TypeFor[flagdef.ProgressiveRollout](),
			reflect.TypeFor[controlplane.ProgressiveRollout](),
			nil,
		},
		{
			"progressive step",
			reflect.TypeFor[flagdef.ProgressiveRolloutStep](),
			reflect.TypeFor[controlplane.ProgressiveRolloutStep](),
			nil,
		},
		{
			"experimentation",
			reflect.TypeFor[dto.ExperimentationDto](),
			reflect.TypeFor[controlplane.Experimentation](),
			nil,
		},
		{
			"experimentation of a step",
			reflect.TypeFor[flagdef.ExperimentationRollout](),
			reflect.TypeFor[controlplane.Experimentation](),
			nil,
		},
		{
			"scheduled step", reflect.TypeFor[flagdef.ScheduledStep](), reflect.TypeFor[controlplane.ScheduledStep](),
			map[string]string{"scheduledRollout": "a step does not nest another schedule in the published format"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			engine, ours := yamlNames(tt.engine), yamlNames(tt.ours)

			for n := range engine {
				if _, skip := tt.exclude[n]; skip {
					continue
				}

				assert.True(t, ours[n], "engine field %q has no counterpart in controlplane", n)
			}

			for n := range ours {
				assert.True(t, engine[n], "controlplane field %q is not an engine field", n)
			}
		})
	}
}

// wantScheduledStep is the engine form of the scheduled step of the "rollouts" case.
func wantScheduledStep() flagdef.ScheduledStep {
	var step flagdef.ScheduledStep

	step.Date = at(3)
	step.Disable = new(true)
	step.Version = new("v3")
	step.BucketingKey = new("k")
	step.TrackEvents = new(true)
	step.Variations = &map[string]*any{"a": anyPtr(1)}
	step.Rules = &[]flagdef.Rule{{Name: new("r"), VariationResult: new("a")}}
	step.DefaultRule = &flagdef.Rule{VariationResult: new("b")}
	step.Experimentation = &flagdef.ExperimentationRollout{Start: at(4)}
	step.Metadata = &map[string]any{"m": 1}

	return step
}
