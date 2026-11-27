package controlplane_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

func validManifest() *controlplane.Manifest {
	m := fixtureManifest()
	m.APIVersion, m.Kind = controlplane.APIVersion, controlplane.DocumentKindManifest

	return m
}

func validFlagSet() *controlplane.FlagSetDocument {
	d := fixtureFlagSet()
	d.APIVersion, d.Kind = controlplane.APIVersion, controlplane.DocumentKindFlagSet

	return d
}

// problem is the machine-readable part of a FieldError, as a test expects it.
type problem struct {
	code  controlplane.Code
	kind  controlplane.EntryKind
	name  string
	path  string
	index int
}

// problemsOf lists the problems of a validation error.
func problemsOf(t *testing.T, err error) []problem {
	t.Helper()

	if err == nil {
		return nil
	}

	var verr *controlplane.ValidationError

	require.ErrorAs(t, err, &verr)

	out := make([]problem, 0, len(verr.Problems))
	for _, p := range verr.Problems {
		out = append(out, problem{code: p.Code, kind: p.Kind, name: p.Name, path: p.Path, index: p.Index})
	}

	return out
}

func obj(code controlplane.Code, name string, index int, path string) problem {
	return problem{code: code, kind: controlplane.EntryObject, name: name, index: index, path: path}
}

func set(code controlplane.Code, name string, index int, path string) problem {
	return problem{code: code, kind: controlplane.EntryFlagSet, name: name, index: index, path: path}
}

func flagProblem(code controlplane.Code, name, path string) problem {
	return problem{code: code, kind: controlplane.EntryFlag, name: name, index: controlplane.NoIndex, path: path}
}

func docProblem(code controlplane.Code, path string) problem {
	return problem{code: code, kind: controlplane.EntryDocument, name: "", index: controlplane.NoIndex, path: path}
}

func TestValidateManifest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mutate func(*controlplane.Manifest)
		name   string
		opts   []controlplane.ValidateOption
		want   []problem
	}{
		{name: "valid manifest", mutate: func(*controlplane.Manifest) {}},
		{name: "empty objects is valid", mutate: func(m *controlplane.Manifest) { m.Objects = nil }},
		{
			name:   "size zero is valid: the reader marks the entry, the publisher refuses to write it",
			mutate: func(m *controlplane.Manifest) { m.Objects[0].Size = 0 },
		},
		{
			name:   "no channel",
			mutate: func(m *controlplane.Manifest) { m.Channel = "" },
			want:   []problem{docProblem(controlplane.CodeMissingValue, "channel")},
		},
		{
			name:   "generation zero",
			mutate: func(m *controlplane.Manifest) { m.Generation = 0 },
			want:   []problem{docProblem(controlplane.CodeInvalidValue, "generation")},
		},
		{
			name:   "the later object keeps the problem of a duplicate name",
			mutate: func(m *controlplane.Manifest) { m.Objects[1].Name = m.Objects[0].Name },
			want:   []problem{obj(controlplane.CodeDuplicateName, nameCheckoutFlags, 1, "objects[1].name")},
		},
		{
			name:   "bad name",
			mutate: func(m *controlplane.Manifest) { m.Objects[0].Name = "Bad_Name" },
			want:   []problem{obj(controlplane.CodeInvalidName, "Bad_Name", 0, pathObject0Name)},
		},
		{
			name:   "long name",
			mutate: func(m *controlplane.Manifest) { m.Objects[0].Name = "a" + strings.Repeat("b", 63) },
			want:   []problem{obj(controlplane.CodeInvalidName, "a"+strings.Repeat("b", 63), 0, pathObject0Name)},
		},
		{
			name:   "digit first",
			mutate: func(m *controlplane.Manifest) { m.Objects[0].Name = "1a" },
			want:   []problem{obj(controlplane.CodeInvalidName, "1a", 0, pathObject0Name)},
		},
		{
			name:   "max length name",
			mutate: func(m *controlplane.Manifest) { m.Objects[0].Name = "a" + strings.Repeat("9", 62) },
		},
		{
			name:   "bad kind",
			mutate: func(m *controlplane.Manifest) { m.Objects[0].Kind = "x" },
			want:   []problem{obj(controlplane.CodeInvalidValue, nameCheckoutFlags, 0, "objects[0].kind")},
		},
		{name: "toml flags", mutate: func(m *controlplane.Manifest) { m.Objects[0].Format = controlplane.FormatTOML }},
		{
			name:   "toml flag sets",
			mutate: func(m *controlplane.Manifest) { m.Objects[1].Format = controlplane.FormatTOML },
			want:   []problem{obj(controlplane.CodeInvalidValue, nameFlagSets, 1, "objects[1].format")},
		},
		{
			name:   "no connection",
			mutate: func(m *controlplane.Manifest) { m.Objects[0].Connection = "" },
			want:   []problem{obj(controlplane.CodeMissingValue, nameCheckoutFlags, 0, "objects[0].connection")},
		},
		{
			name:   "no key",
			mutate: func(m *controlplane.Manifest) { m.Objects[0].Key = "" },
			want:   []problem{obj(controlplane.CodeMissingValue, nameCheckoutFlags, 0, "objects[0].key")},
		},
		{
			name:   "newer object version is an entry problem",
			mutate: func(m *controlplane.Manifest) { m.Objects[1].Version = 2 },
			want:   []problem{obj(controlplane.CodeUnsupportedVersion, nameFlagSets, 1, "objects[1].version")},
		},
		{
			name:   "negative size",
			mutate: func(m *controlplane.Manifest) { m.Objects[0].Size = -1 },
			want:   []problem{obj(controlplane.CodeInvalidValue, nameCheckoutFlags, 0, "objects[0].size")},
		},
		{
			name:   "md5",
			mutate: func(m *controlplane.Manifest) { m.Objects[0].Checksum.Algorithm = algoMD5 },
			want:   []problem{obj(controlplane.CodeInvalidChecksum, nameCheckoutFlags, 0, "objects[0].checksum")},
		},
		{
			name:   "short digest",
			mutate: func(m *controlplane.Manifest) { m.Objects[0].Checksum.Value = "ab" },
			want:   []problem{obj(controlplane.CodeInvalidChecksum, nameCheckoutFlags, 0, "objects[0].checksum")},
		},
		{
			name:   "upper digest",
			mutate: func(m *controlplane.Manifest) { m.Objects[0].Checksum.Value = strings.ToUpper(abcSum) },
			want:   []problem{obj(controlplane.CodeInvalidChecksum, nameCheckoutFlags, 0, "objects[0].checksum")},
		},
		{
			name:   "limit option",
			mutate: func(*controlplane.Manifest) {},
			opts:   []controlplane.ValidateOption{controlplane.WithMaxObjects(1)},
			want:   []problem{docProblem(controlplane.CodeTooManyObjects, "objects")},
		},
		{
			name:   "raised limit option",
			mutate: func(*controlplane.Manifest) {},
			opts:   []controlplane.ValidateOption{controlplane.WithMaxObjects(2)},
		},
		{
			name:   "non positive option keeps the default",
			mutate: func(*controlplane.Manifest) {},
			opts:   []controlplane.ValidateOption{controlplane.WithMaxObjects(0)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := validManifest()
			tt.mutate(m)

			assert.Equal(t, tt.want, problemsOf(t, controlplane.ValidateManifest(m, tt.opts...)))
		})
	}
}

func TestDefaultMaxObjects(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 20000, controlplane.DefaultMaxObjects)

	manifest := validManifest()
	manifest.Objects = make([]controlplane.ManifestObject, controlplane.DefaultMaxObjects+1)

	var verr *controlplane.ValidationError

	require.ErrorAs(t, controlplane.ValidateManifest(manifest), &verr)
	assert.Equal(t, controlplane.CodeTooManyObjects, verr.Problems[0].Code)

	manifest.Objects = manifest.Objects[:controlplane.DefaultMaxObjects]
	require.ErrorAs(t, controlplane.ValidateManifest(manifest), &verr)
	assert.NotEqual(t, controlplane.CodeTooManyObjects, verr.Problems[0].Code)
}

func TestValidateHeaderErrors(t *testing.T) {
	t.Parallel()

	manifestWith := func(apiVersion, kind string) error {
		m := validManifest()
		m.APIVersion, m.Kind = apiVersion, kind

		return controlplane.ValidateManifest(m)
	}

	setWith := func(apiVersion, kind string) error {
		d := validFlagSet()
		d.APIVersion, d.Kind = apiVersion, kind

		return controlplane.ValidateFlagSet(d, validManifest())
	}

	tests := []struct {
		err         error
		wantSpecial error
		name        string
	}{
		{manifestWith(controlplane.APIVersion, "FlagSet"), controlplane.ErrWrongKind, "manifest of another kind"},
		{
			manifestWith("semaphore/v2", controlplane.DocumentKindManifest),
			controlplane.ErrUnsupportedAPIVersion,
			"manifest of another version",
		},
		{setWith(controlplane.APIVersion, "Manifest"), controlplane.ErrWrongKind, "flag set of another kind"},
		{
			setWith("x", controlplane.DocumentKindFlagSet),
			controlplane.ErrUnsupportedAPIVersion,
			"flag set of another version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.ErrorIs(t, tt.err, tt.wantSpecial)
			require.ErrorIs(t, tt.err, controlplane.ErrInvalid, "a header failure is an invalid document too")

			var version *controlplane.UnsupportedVersionError

			var kind *controlplane.KindError

			switch {
			case errors.As(tt.err, &version):
				assert.Equal(t, controlplane.APIVersion, version.Supported)
				assert.NotEmpty(t, version.Got)
			case errors.As(tt.err, &kind):
				assert.NotEmpty(t, kind.Want)
				assert.NotEqual(t, kind.Want, kind.Got)
			default:
				assert.Fail(t, "a typed error is expected")
			}
		})
	}
}

func TestValidateFlagSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mutate func(*controlplane.FlagSetDocument)
		name   string
		want   []problem
		noMani bool
	}{
		{name: "valid flag set", mutate: func(*controlplane.FlagSetDocument) {}},
		{name: "empty list is valid", mutate: func(d *controlplane.FlagSetDocument) { d.FlagSets = nil }},
		{
			name:   "bad name",
			mutate: func(d *controlplane.FlagSetDocument) { d.FlagSets[0].Name = "Up" },
			want:   []problem{set(controlplane.CodeInvalidName, "Up", 0, "flagSets[0].name")},
		},
		{
			name:   "the later flag set has the duplicate name problem",
			mutate: func(d *controlplane.FlagSetDocument) { d.FlagSets[1].Name = d.FlagSets[0].Name },
			want:   []problem{set(controlplane.CodeDuplicateName, "checkout", 1, "flagSets[1].name")},
		},
		{
			name:   "no sources",
			mutate: func(d *controlplane.FlagSetDocument) { d.FlagSets[0].Sources = nil },
			want:   []problem{set(controlplane.CodeMissingValue, "checkout", 0, "flagSets[0].sources")},
		},
		{
			name:   "unresolved source",
			mutate: func(d *controlplane.FlagSetDocument) { d.FlagSets[0].Sources = []string{sourceNope} },
			want:   []problem{set(controlplane.CodeUnresolvedSource, "checkout", 0, "flagSets[0].sources[0]")},
		},
		{
			name:   "source of kind flagSets",
			mutate: func(d *controlplane.FlagSetDocument) { d.FlagSets[0].Sources = []string{nameFlagSets} },
			want:   []problem{set(controlplane.CodeUnresolvedSource, "checkout", 0, "flagSets[0].sources[0]")},
		},
		{
			name:   "unresolved without manifest",
			mutate: func(d *controlplane.FlagSetDocument) { d.FlagSets[0].Sources = []string{sourceNope} },
			noMani: true,
		},
		{
			name:   "empty key",
			mutate: func(d *controlplane.FlagSetDocument) { d.FlagSets[1].APIKeys = []string{""} },
			want:   []problem{set(controlplane.CodeInvalidValue, "no-keys-yet", 1, "flagSets[1].apiKeys[0]")},
		},
		{
			name:   "the first flag set keeps a duplicate key, the later one has the problem",
			mutate: func(d *controlplane.FlagSetDocument) { d.FlagSets[1].APIKeys = d.FlagSets[0].APIKeys },
			want:   []problem{set(controlplane.CodeDuplicateKey, "no-keys-yet", 1, "flagSets[1].apiKeys[0]")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := validFlagSet()
			tt.mutate(doc)

			manifest := validManifest()
			if tt.noMani {
				manifest = nil
			}

			err := controlplane.ValidateFlagSet(doc, manifest)
			assert.Equal(t, tt.want, problemsOf(t, err))

			if err != nil {
				assert.NotContains(t, err.Error(), "6f1f1b7e", "an API key is never printed")
			}
		})
	}
}

type flagCase struct {
	mutate func(controlplane.Flags)
	name   string
	want   []problem
}

func goodFlags() controlplane.Flags {
	return controlplane.Flags{"f": {
		Variations:  map[string]any{"a": 1, "b": 2},
		DefaultRule: &controlplane.Rule{Variation: new("a")},
		Targeting: []controlplane.Rule{
			{Query: new(queryXEq1), Percentage: map[string]float64{"a": 50, "b": 50}},
		},
	}}
}

func edit(flags controlplane.Flags, change func(*controlplane.Flag)) {
	flag := flags["f"]
	change(&flag)
	flags["f"] = flag
}

func runFlagCases(t *testing.T, cases []flagCase) {
	t.Helper()

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flags := goodFlags()
			tt.mutate(flags)

			assert.Equal(t, tt.want, problemsOf(t, controlplane.ValidateFlags(flags)))
		})
	}
}

func TestValidateFlagsVariations(t *testing.T) {
	t.Parallel()

	runs := []flagCase{
		{name: "valid flags", mutate: func(controlplane.Flags) {}},
		{
			name:   "empty name",
			mutate: func(flags controlplane.Flags) { flags[""] = flags["f"] },
			want:   []problem{flagProblem(controlplane.CodeInvalidName, "", "")},
		},
		{
			name:   "no variations",
			mutate: func(flags controlplane.Flags) { edit(flags, func(x *controlplane.Flag) { x.Variations = nil }) },
			want: []problem{
				flagProblem(controlplane.CodeMissingValue, "f", "f.variations"),
				flagProblem(controlplane.CodeUndeclaredVariation, "f", "f.defaultRule.variation"),
				flagProblem(controlplane.CodeUndeclaredVariation, "f", "f.targeting[0].percentage.a"),
				flagProblem(controlplane.CodeUndeclaredVariation, "f", "f.targeting[0].percentage.b"),
			},
		},
		{
			name:   "no default rule",
			mutate: func(flags controlplane.Flags) { edit(flags, func(x *controlplane.Flag) { x.DefaultRule = nil }) },
			want:   []problem{flagProblem(controlplane.CodeMissingValue, "f", "f.defaultRule")},
		},
		{
			name: "converter alone is not a flag",
			mutate: func(flags controlplane.Flags) {
				flags["c"] = controlplane.Flag{Converter: new("kayenta")}
			},
			want: []problem{
				flagProblem(controlplane.CodeMissingValue, "c", "c.variations"),
				flagProblem(controlplane.CodeMissingValue, "c", "c.defaultRule"),
			},
		},
		{
			name:   "empty variation names are valid",
			mutate: func(flags controlplane.Flags) { flags["e"] = emptyNamedFlag() },
		},
		{
			name: "null variation",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) { x.Variations = map[string]any{"a": 1, "b": nil} })
			},
			want: []problem{flagProblem(controlplane.CodeInvalidValue, "f", "f.variations.b")},
		},
		{
			name: "mixed variation types",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) { x.Variations = map[string]any{"a": 1, "b": setTwo} })
			},
			want: []problem{flagProblem(controlplane.CodeInvalidValue, "f", "f.variations.b")},
		},
		{
			name: "ints and floats are one type",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) { x.Variations = map[string]any{"a": 1, "b": 2.5} })
			},
		},
	}

	runFlagCases(t, runs)
}

func TestValidateFlagsRules(t *testing.T) {
	t.Parallel()

	runs := []flagCase{
		{
			name: "undeclared default variation",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) { x.DefaultRule = &controlplane.Rule{Variation: new("z")} })
			},
			want: []problem{flagProblem(controlplane.CodeUndeclaredVariation, "f", "f.defaultRule.variation")},
		},
		{
			name: "undeclared percentage variation",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) {
					x.Targeting = []controlplane.Rule{{Query: new(queryXEq1), Percentage: map[string]float64{"z": 1}}}
				})
			},
			want: []problem{flagProblem(controlplane.CodeUndeclaredVariation, "f", "f.targeting[0].percentage.z")},
		},
		{
			name: "percentage above 100 is allowed",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) {
					x.Targeting = []controlplane.Rule{{Query: new(queryXEq1), Percentage: map[string]float64{"a": 101}}}
				})
			},
		},
		{
			name: "negative percentage",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) {
					x.Targeting = []controlplane.Rule{
						{Query: new(queryXEq1), Percentage: map[string]float64{"a": -1, "b": 5}},
					}
				})
			},
			want: []problem{flagProblem(controlplane.CodeInvalidValue, "f", "f.targeting[0].percentage.a")},
		},
		{
			name: "percentages that sum to zero",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) {
					x.Targeting = []controlplane.Rule{
						{Query: new(queryXEq1), Percentage: map[string]float64{"a": 0, "b": 0}},
					}
				})
			},
			want: []problem{flagProblem(controlplane.CodeInvalidValue, "f", "f.targeting[0].percentage")},
		},
		{
			name: "empty percentage map",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) {
					x.Targeting = []controlplane.Rule{{Query: new(queryXEq1), Percentage: map[string]float64{}}}
				})
			},
			want: []problem{flagProblem(controlplane.CodeInvalidValue, "f", "f.targeting[0].percentage")},
		},
		{
			name: "rule without outcome",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) { x.Targeting = []controlplane.Rule{{Query: new(queryXEq1)}} })
			},
			want: []problem{flagProblem(controlplane.CodeMissingValue, "f", "f.targeting[0]")},
		},
		{
			name: "rule without query",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) { x.Targeting = []controlplane.Rule{{Variation: new("a")}} })
			},
			want: []problem{flagProblem(controlplane.CodeMissingValue, "f", "f.targeting[0].query")},
		},
		{
			name: "a disabled rule needs nothing",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) { x.Targeting = []controlplane.Rule{{Disable: new(true)}} })
			},
		},
	}

	runFlagCases(t, runs)
}

func TestValidateFlagsRollouts(t *testing.T) {
	t.Parallel()

	runs := []flagCase{
		{
			name: "progressive without end",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) {
					x.Targeting = []controlplane.Rule{
						{Query: new(queryXEq1), ProgressiveRollout: &controlplane.ProgressiveRollout{
							Initial: &controlplane.ProgressiveRolloutStep{Variation: new("a")},
						}},
					}
				})
			},
			want: []problem{flagProblem(controlplane.CodeMissingValue, "f", "f.targeting[0].progressiveRollout")},
		},
		{
			name: "progressive without dates and variations",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) {
					x.Targeting = []controlplane.Rule{
						{Query: new(queryXEq1), ProgressiveRollout: &controlplane.ProgressiveRollout{
							Initial: &controlplane.ProgressiveRolloutStep{},
							End:     &controlplane.ProgressiveRolloutStep{},
						}},
					}
				})
			},
			want: []problem{
				flagProblem(controlplane.CodeMissingValue, "f", "f.targeting[0].progressiveRollout.initial.variation"),
				flagProblem(controlplane.CodeMissingValue, "f", "f.targeting[0].progressiveRollout.initial.date"),
				flagProblem(controlplane.CodeMissingValue, "f", "f.targeting[0].progressiveRollout.end.variation"),
				flagProblem(controlplane.CodeMissingValue, "f", "f.targeting[0].progressiveRollout.end.date"),
			},
		},
		{
			name: "progressive with reversed dates and undeclared variation",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) {
					x.Targeting = []controlplane.Rule{
						{Query: new(queryXEq1), ProgressiveRollout: &controlplane.ProgressiveRollout{
							Initial: &controlplane.ProgressiveRolloutStep{
								Variation:  new("a"),
								Date:       fixtureTime(2),
								Percentage: new(50.0),
							},
							End: &controlplane.ProgressiveRolloutStep{
								Variation:  new("q"),
								Date:       fixtureTime(1),
								Percentage: new(10.0),
							},
						}},
					}
				})
			},
			want: []problem{
				flagProblem(
					controlplane.CodeUndeclaredVariation,
					"f",
					"f.targeting[0].progressiveRollout.end.variation",
				),
				flagProblem(controlplane.CodeInvalidValue, "f", "f.targeting[0].progressiveRollout.end.date"),
				flagProblem(controlplane.CodeInvalidValue, "f", "f.targeting[0].progressiveRollout.end.percentage"),
			},
		},
		{
			name: "scheduled step without a date",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) {
					x.ScheduledRollout = []controlplane.ScheduledStep{
						{DefaultRule: &controlplane.Rule{Variation: new("b")}},
					}
				})
			},
			want: []problem{flagProblem(controlplane.CodeMissingValue, "f", "f.scheduledRollout[0].date")},
		},
		{
			name: "scheduled step with an undeclared variation",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) {
					x.ScheduledRollout = []controlplane.ScheduledStep{
						{Date: fixtureTime(1), DefaultRule: &controlplane.Rule{Variation: new("q")}},
					}
				})
			},
			want: []problem{
				flagProblem(controlplane.CodeUndeclaredVariation, "f", "f.scheduledRollout[0].defaultRule.variation"),
			},
		},
		{
			name: "scheduled step may update a named rule partially",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) {
					x.ScheduledRollout = []controlplane.ScheduledStep{{
						Date:      fixtureTime(1),
						Targeting: []controlplane.Rule{{Name: new("beta"), Percentage: map[string]float64{"a": 1}}},
					}}
				})
			},
		},
		{
			name: "scheduled step with its own variations",
			mutate: func(flags controlplane.Flags) {
				edit(flags, func(x *controlplane.Flag) {
					x.ScheduledRollout = []controlplane.ScheduledStep{{
						Date:        fixtureTime(1),
						Variations:  map[string]any{"q": 1, "r": setTwo},
						DefaultRule: &controlplane.Rule{Variation: new("q")},
					}}
				})
			},
			want: []problem{flagProblem(controlplane.CodeInvalidValue, "f", "f.scheduledRollout[0].variations.r")},
		},
	}

	runFlagCases(t, runs)
}

// emptyNamedFlag declares a variation called "" and uses it; the engine accepts that.
func emptyNamedFlag() controlplane.Flag {
	return controlplane.Flag{
		Variations:  map[string]any{"": "x", "y": "z"},
		DefaultRule: &controlplane.Rule{Variation: new("")},
	}
}
