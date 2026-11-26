package controlplane_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

func TestUnmarshalManifest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		in          string
		wantErr     error
		wantChannel string
		wantUnknown []string
	}{
		{
			controlplane.FormatYAML,
			"apiVersion: semaphore/v1\nkind: Manifest\nchannel: stable\n",
			nil,
			channelStable,
			nil,
		},
		{
			controlplane.FormatJSON,
			`{"apiVersion":"semaphore/v1","kind":"Manifest","channel":"canary"}`,
			nil,
			"canary",
			nil,
		},
		{
			"json with leading space",
			" \n\t{\"apiVersion\":\"semaphore/v1\",\"kind\":\"Manifest\",\"channel\":\"c\"}",
			nil,
			"c",
			nil,
		},
		{
			"unknown fields counted",
			"apiVersion: semaphore/v1\nkind: Manifest\nz: 1\na:\n  b: 2\nobjects:\n  - name: x\n    extra: 1\n",
			nil,
			"",
			[]string{"a", "objects[0].extra", "z"},
		},
		{
			"unknown fields in json",
			`{"apiVersion":"semaphore/v1","kind":"Manifest","later":{"x":1}}`,
			nil,
			"",
			[]string{"later"},
		},
		{"another kind", "apiVersion: semaphore/v1\nkind: FlagSet\n", controlplane.ErrWrongKind, "", nil},
		{"headerless", "channel: stable\n", controlplane.ErrWrongKind, "", nil},
		{"other major", "apiVersion: semaphore/v2\nkind: Manifest\n", controlplane.ErrUnsupportedAPIVersion, "", nil},
		{"syntax", "apiVersion: [\n", controlplane.ErrDecode, "", nil},
		{"wrong type", "apiVersion: semaphore/v1\nkind: Manifest\ngeneration: many\n", controlplane.ErrDecode, "", nil},
		{"json syntax", "{", controlplane.ErrDecode, "", nil},
		{"empty document", "", controlplane.ErrWrongKind, "", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, info, err := controlplane.UnmarshalManifest([]byte(tt.in))
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantChannel, got.Channel)
			assert.Equal(t, tt.wantUnknown, info.UnknownFields)
		})
	}
}

func TestUnmarshalFlagSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		in          string
		wantErr     error
		wantNames   []string
		wantUnknown []string
	}{
		{
			controlplane.FormatYAML,
			"apiVersion: semaphore/v1\nkind: FlagSet\nflagSets:\n  - name: a\n  - name: b\n",
			nil,
			[]string{"a", "b"},
			nil,
		},
		{
			"unknown",
			"apiVersion: semaphore/v1\nkind: FlagSet\nflagSets:\n  - name: a\n    tier: 1\n",
			nil,
			[]string{"a"},
			[]string{"flagSets[0].tier"},
		},
		{"manifest handed in", "apiVersion: semaphore/v1\nkind: Manifest\n", controlplane.ErrWrongKind, nil, nil},
		{"other major", "apiVersion: semaphore/v3\nkind: FlagSet\n", controlplane.ErrUnsupportedAPIVersion, nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, info, err := controlplane.UnmarshalFlagSet([]byte(tt.in))
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)

			var names []string
			for _, fs := range got.FlagSets {
				names = append(names, fs.Name)
			}

			assert.Equal(t, tt.wantNames, names)
			assert.Equal(t, tt.wantUnknown, info.UnknownFields)
		})
	}
}

func TestUnmarshalFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		in          string
		format      string
		wantErr     error
		wantFlags   []string
		wantUnknown []string
	}{
		{"headerless yaml is v1", "f:\n  variations:\n    a: 1\n", controlplane.FormatYAML, nil, []string{"f"}, nil},
		{"default format is yaml", "f:\n  variations:\n    a: 1\n", "", nil, []string{"f"}, nil},
		{"yml alias", "f: {}\n", "YML", nil, []string{"f"}, nil},
		{controlplane.FormatJSON, `{"f":{"variations":{"a":1}}}`, controlplane.FormatJSON, nil, []string{"f"}, nil},
		{"empty file", "", controlplane.FormatYAML, nil, nil, nil},
		{
			"unknown nested",
			"f:\n  nope: 1\n  defaultRule:\n    nah: 1\n  variations:\n    free: {any: thing}\n",
			controlplane.FormatYAML,
			nil,
			[]string{"f"},
			[]string{"f.defaultRule.nah", "f.nope"},
		},
		{"toml is not read", "[f]\n", controlplane.FormatTOML, controlplane.ErrUnsupportedFormat, nil, nil},
		{"syntax", "f: [", controlplane.FormatYAML, controlplane.ErrDecode, nil, nil},
		{"wrong type", "f: 3\n", controlplane.FormatYAML, controlplane.ErrDecode, nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, info, err := controlplane.UnmarshalFlags([]byte(tt.in), tt.format)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)

			var names []string
			for n := range got {
				names = append(names, n)
			}

			assert.ElementsMatch(t, tt.wantFlags, names)
			assert.Equal(t, tt.wantUnknown, info.UnknownFields)
		})
	}
}
