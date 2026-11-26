package controlplane_test

import (
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

func TestValidateManifest(t *testing.T) {
	t.Parallel()

	tests := []struct { //nolint:govet // fieldalignment: the columns of the table are in reading order
		name     string
		mutate   func(*controlplane.Manifest)
		opts     []controlplane.ValidateOption
		wantErr  error
		wantPath string
	}{
		{"valid manifest", func(*controlplane.Manifest) {}, nil, nil, ""},
		{"empty objects is valid", func(m *controlplane.Manifest) { m.Objects = nil }, nil, nil, ""},
		{"wrong kind", func(m *controlplane.Manifest) { m.Kind = "FlagSet" }, nil, controlplane.ErrWrongKind, ""},
		{
			"other version",
			func(m *controlplane.Manifest) { m.APIVersion = "semaphore/v2" },
			nil,
			controlplane.ErrUnsupportedAPIVersion,
			"",
		},
		{"no channel", func(m *controlplane.Manifest) { m.Channel = "" }, nil, controlplane.ErrInvalid, "channel"},
		{
			"generation zero",
			func(m *controlplane.Manifest) { m.Generation = 0 },
			nil,
			controlplane.ErrInvalid,
			"generation",
		},
		{
			"duplicate name",
			func(m *controlplane.Manifest) { m.Objects[1].Name = m.Objects[0].Name },
			nil,
			controlplane.ErrInvalid,
			"objects[1].name",
		},
		{
			"bad name",
			func(m *controlplane.Manifest) { m.Objects[0].Name = "Bad_Name" },
			nil,
			controlplane.ErrInvalid,
			pathObject0Name,
		},
		{
			"long name",
			func(m *controlplane.Manifest) { m.Objects[0].Name = "a" + strings.Repeat("b", 63) },
			nil,
			controlplane.ErrInvalid,
			pathObject0Name,
		},
		{
			"digit first",
			func(m *controlplane.Manifest) { m.Objects[0].Name = "1a" },
			nil,
			controlplane.ErrInvalid,
			pathObject0Name,
		},
		{
			"max length name",
			func(m *controlplane.Manifest) { m.Objects[0].Name = "a" + strings.Repeat("9", 62) },
			nil,
			nil,
			"",
		},
		{
			"bad kind",
			func(m *controlplane.Manifest) { m.Objects[0].Kind = "x" },
			nil,
			controlplane.ErrInvalid,
			"objects[0].kind",
		},
		{"toml flags", func(m *controlplane.Manifest) { m.Objects[0].Format = controlplane.FormatTOML }, nil, nil, ""},
		{
			"toml flag sets",
			func(m *controlplane.Manifest) { m.Objects[1].Format = controlplane.FormatTOML },
			nil,
			controlplane.ErrInvalid,
			"objects[1].format",
		},
		{
			"bad flags format",
			func(m *controlplane.Manifest) { m.Objects[0].Format = "xml" },
			nil,
			controlplane.ErrInvalid,
			"objects[0].format",
		},
		{
			"no connection",
			func(m *controlplane.Manifest) { m.Objects[0].Connection = "" },
			nil,
			controlplane.ErrInvalid,
			"objects[0].connection",
		},
		{
			"no key",
			func(m *controlplane.Manifest) { m.Objects[0].Key = "" },
			nil,
			controlplane.ErrInvalid,
			"objects[0].key",
		},
		{
			"object version 2",
			func(m *controlplane.Manifest) { m.Objects[0].Version = 2 },
			nil,
			controlplane.ErrInvalid,
			"objects[0].version",
		},
		{
			"no size",
			func(m *controlplane.Manifest) { m.Objects[0].Size = 0 },
			nil,
			controlplane.ErrInvalid,
			"objects[0].size",
		},
		{
			algoMD5,
			func(m *controlplane.Manifest) { m.Objects[0].Checksum.Algorithm = algoMD5 },
			nil,
			controlplane.ErrInvalid,
			"objects[0].checksum.algorithm",
		},
		{
			"short digest",
			func(m *controlplane.Manifest) { m.Objects[0].Checksum.Value = "ab" },
			nil,
			controlplane.ErrInvalid,
			"objects[0].checksum.value",
		},
		{
			"upper digest",
			func(m *controlplane.Manifest) { m.Objects[0].Checksum.Value = strings.ToUpper(abcSum) },
			nil,
			controlplane.ErrInvalid,
			"objects[0].checksum.value",
		},
		{"over default limit", func(m *controlplane.Manifest) {
			m.Objects = make([]controlplane.ManifestObject, controlplane.DefaultMaxObjects+1)
		}, nil, controlplane.ErrInvalid, "objects:"},
		{
			"limit option",
			func(*controlplane.Manifest) {},
			[]controlplane.ValidateOption{controlplane.WithMaxObjects(1)},
			controlplane.ErrInvalid,
			"objects:",
		},
		{
			"raised limit option",
			func(*controlplane.Manifest) {},
			[]controlplane.ValidateOption{controlplane.WithMaxObjects(2)},
			nil,
			"",
		},
		{
			"non positive option keeps default",
			func(*controlplane.Manifest) {},
			[]controlplane.ValidateOption{controlplane.WithMaxObjects(0)},
			nil,
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := validManifest()
			tt.mutate(m)

			err := controlplane.ValidateManifest(m, tt.opts...)
			if tt.wantErr == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tt.wantErr)
			assert.Contains(t, err.Error(), tt.wantPath)
		})
	}

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		require.ErrorIs(t, controlplane.ValidateManifest(nil), controlplane.ErrNilDocument)
	})

	t.Run("lists every problem", func(t *testing.T) {
		t.Parallel()

		m := validManifest()
		m.Channel, m.Generation = "", 0

		var verr *controlplane.ValidationError

		require.ErrorAs(t, controlplane.ValidateManifest(m), &verr)
		assert.Equal(t, []controlplane.FieldError{
			{Path: "channel", Reason: "is required"},
			{Path: "generation", Reason: "must be at least 1"},
		}, verr.Problems)
	})
}

func TestValidateFlagSet(t *testing.T) {
	t.Parallel()

	tests := []struct { //nolint:govet // fieldalignment: the columns of the table are in reading order
		name     string
		mutate   func(*controlplane.FlagSetDocument)
		noMani   bool
		wantErr  error
		wantPath string
	}{
		{"valid flag set", func(*controlplane.FlagSetDocument) {}, false, nil, ""},
		{"empty list is valid", func(d *controlplane.FlagSetDocument) { d.FlagSets = nil }, false, nil, ""},
		{
			"wrong kind",
			func(d *controlplane.FlagSetDocument) { d.Kind = "Manifest" },
			false,
			controlplane.ErrWrongKind,
			"",
		},
		{
			"other version",
			func(d *controlplane.FlagSetDocument) { d.APIVersion = "x" },
			false,
			controlplane.ErrUnsupportedAPIVersion,
			"",
		},
		{
			"bad name",
			func(d *controlplane.FlagSetDocument) { d.FlagSets[0].Name = "Up" },
			false,
			controlplane.ErrInvalid,
			"flagSets[0].name",
		},
		{
			"duplicate name",
			func(d *controlplane.FlagSetDocument) { d.FlagSets[1].Name = d.FlagSets[0].Name },
			false,
			controlplane.ErrInvalid,
			"flagSets[1].name",
		},
		{
			"no sources",
			func(d *controlplane.FlagSetDocument) { d.FlagSets[0].Sources = nil },
			false,
			controlplane.ErrInvalid,
			"flagSets[0].sources",
		},
		{
			"unresolved source",
			func(d *controlplane.FlagSetDocument) { d.FlagSets[0].Sources = []string{"nope"} },
			false,
			controlplane.ErrInvalid,
			"flagSets[0].sources[0]",
		},
		{
			"source of kind flagSets",
			func(d *controlplane.FlagSetDocument) { d.FlagSets[0].Sources = []string{nameFlagSets} },
			false,
			controlplane.ErrInvalid,
			"flagSets[0].sources[0]",
		},
		{
			"unresolved without manifest",
			func(d *controlplane.FlagSetDocument) { d.FlagSets[0].Sources = []string{"nope"} },
			true,
			nil,
			"",
		},
		{
			"empty key",
			func(d *controlplane.FlagSetDocument) { d.FlagSets[1].APIKeys = []string{""} },
			false,
			controlplane.ErrInvalid,
			"flagSets[1].apiKeys[0]",
		},
		{
			"duplicate key",
			func(d *controlplane.FlagSetDocument) { d.FlagSets[1].APIKeys = d.FlagSets[0].APIKeys },
			false,
			controlplane.ErrInvalid,
			"flagSets[1].apiKeys[0]: duplicate of flagSets[0].apiKeys[0]",
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
			if tt.wantErr == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tt.wantErr)
			assert.Contains(t, err.Error(), tt.wantPath)
			assert.NotContains(t, err.Error(), "6f1f1b7e", "an API key is never printed")
		})
	}

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		require.ErrorIs(t, controlplane.ValidateFlagSet(nil, nil), controlplane.ErrNilDocument)
	})
}

func TestValidateFlags(t *testing.T) {
	t.Parallel()

	good := func() controlplane.Flags {
		return controlplane.Flags{"f": {
			Variations:  map[string]any{"a": 1, "b": 2},
			DefaultRule: &controlplane.Rule{Variation: "a"},
			Targeting:   []controlplane.Rule{{Query: queryXEq1, Percentage: map[string]float64{"a": 50, "b": 50}}},
		}}
	}

	tests := []struct {
		name     string
		mutate   func(controlplane.Flags)
		wantPath string
	}{
		{"valid flags", func(controlplane.Flags) {}, ""},
		{"empty name", func(f controlplane.Flags) { f[""] = f["f"] }, ": flag name is empty"},
		{
			"no variations",
			func(f controlplane.Flags) { fl := f["f"]; fl.Variations = nil; f["f"] = fl },
			"f.variations",
		},
		{
			"no default rule",
			func(f controlplane.Flags) { fl := f["f"]; fl.DefaultRule = nil; f["f"] = fl },
			"f.defaultRule",
		},
		{
			"converter needs neither",
			func(f controlplane.Flags) { f["c"] = controlplane.Flag{Converter: "kayenta"} },
			"",
		},
		{"unknown variation", func(f controlplane.Flags) {
			fl := f["f"]
			fl.DefaultRule = &controlplane.Rule{Variation: "z"}
			f["f"] = fl
		}, "f.defaultRule.variation"},
		{"unknown percentage variation", func(f controlplane.Flags) {
			fl := f["f"]
			fl.Targeting = []controlplane.Rule{{Percentage: map[string]float64{"z": 1}}}
			f["f"] = fl
		}, "f.targeting[0].percentage.z"},
		{"percentage over 100", func(f controlplane.Flags) {
			fl := f["f"]
			fl.Targeting = []controlplane.Rule{{Percentage: map[string]float64{"a": 101}}}
			f["f"] = fl
		}, "f.targeting[0].percentage.a"},
		{"negative percentage", func(f controlplane.Flags) {
			fl := f["f"]
			fl.Targeting = []controlplane.Rule{{Percentage: map[string]float64{"a": -1}}}
			f["f"] = fl
		}, "f.targeting[0].percentage.a"},
		{"empty rule", func(f controlplane.Flags) {
			fl := f["f"]
			fl.Targeting = []controlplane.Rule{{Query: queryXEq1}}
			f["f"] = fl
		}, "f.targeting[0]: needs a variation"},
		{"progressive without end", func(flags controlplane.Flags) {
			flag := flags["f"]
			flag.Targeting = []controlplane.Rule{{ProgressiveRollout: &controlplane.ProgressiveRollout{
				Initial: &controlplane.ProgressiveRolloutStep{Variation: "a"},
			}}}
			flags["f"] = flag
		}, "f.targeting[0].progressiveRollout: needs initial and end"},
		{"progressive unknown variation", func(flags controlplane.Flags) {
			flag := flags["f"]
			flag.Targeting = []controlplane.Rule{{ProgressiveRollout: &controlplane.ProgressiveRollout{
				Initial: &controlplane.ProgressiveRolloutStep{Variation: "a"},
				End:     &controlplane.ProgressiveRolloutStep{Variation: "q"},
			}}}
			flags["f"] = flag
		}, "f.targeting[0].progressiveRollout.end.variation"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := good()
			tt.mutate(f)

			err := controlplane.ValidateFlags(f)
			if tt.wantPath == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, controlplane.ErrInvalid)
			assert.Contains(t, err.Error(), tt.wantPath)
		})
	}
}
