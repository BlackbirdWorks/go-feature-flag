package controlplane_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// untagged has an exported field without a yaml name: writing it would drop the field silently.
type untagged struct {
	Visible string `yaml:"visible"`
	Hidden  string
}

func TestMarshalRefusesToDropFields(t *testing.T) {
	t.Parallel()

	flags := controlplane.Flags{"f": {Metadata: map[string]any{"k": untagged{Visible: "a", Hidden: "b"}}}}

	_, yamlErr := controlplane.MarshalFlags(flags)
	_, jsonErr := controlplane.MarshalFlagsJSON(flags)

	require.ErrorIs(t, yamlErr, controlplane.ErrUnsupportedValue)
	require.ErrorIs(t, jsonErr, controlplane.ErrUnsupportedValue)
	assert.Contains(t, yamlErr.Error(), "f.metadata.k")
}

func TestJSONNumbersAreWrittenAsNumbers(t *testing.T) {
	t.Parallel()

	flags := controlplane.Flags{"f": {Metadata: map[string]any{
		keyInt: json.Number("7"), keyFloat: json.Number("2.5"), "whole": json.Number("2.0"),
		keyHuge: json.Number("12345678901234567890"), keyList: []any{json.Number("1")},
	}}}

	for _, tt := range []struct {
		marshal func(controlplane.Flags) ([]byte, error)
		format  string
	}{{controlplane.MarshalFlags, controlplane.FormatYAML}, {controlplane.MarshalFlagsJSON, controlplane.FormatJSON}} {
		data, err := tt.marshal(flags)
		require.NoError(t, err)

		got, _, err := controlplane.UnmarshalFlags(data, tt.format)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			keyInt: 7, keyFloat: 2.5, "whole": 2.0, keyHuge: hugeUnsigned, keyList: []any{1},
		}, got["f"].Metadata, "%s", data)
	}

	for _, bad := range []string{"abc", "", "1e999", "123456789012345678901234567890"} {
		_, err := controlplane.MarshalFlags(controlplane.Flags{"f": {Metadata: map[string]any{"k": json.Number(bad)}}})
		require.ErrorIs(t, err, controlplane.ErrUnsupportedValue, bad)
	}
}

func TestVariationTypesMirrorTheEngine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		variations map[string]any
		name       string
		wantValid  bool
	}{
		{map[string]any{"a": 1, "b": 2.5}, "int and float", true},
		{map[string]any{"a": int8(1), "b": uint16(2), "c": int64(3), "d": float32(1.5)}, "sized ints", true},
		{map[string]any{"a": uint64(5), "b": 1}, "uint64 within int64", true},
		{map[string]any{"a": uint64(math.MaxInt64) + 1, "b": 1}, "uint64 above int64", false},
		{map[string]any{"a": hugeUnsigned}, "only uint64 above int64", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := controlplane.ValidateFlags(controlplane.Flags{"f": {
				Variations: tt.variations, DefaultRule: &controlplane.Rule{Percentage: map[string]float64{"a": 1}},
			}})

			if tt.wantValid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, controlplane.ErrInvalid)
			}
		})
	}
}

func TestMergeKeysCannotLoopOrHideTheHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		wantErr bool
	}{
		{"self merge", "apiVersion: semaphore/v1\nkind: Manifest\n<<: &a\n  <<: *a\n", true},
		{"merge of a list that contains itself", "apiVersion: semaphore/v1\nkind: Manifest\n<<: &a\n  - *a\n", true},
		{
			"header from a merge",
			"<<: &h {apiVersion: semaphore/v1, kind: Manifest}\nchannel: c\ngeneration: 1\nobjects: []\n",
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, _, err := controlplane.UnmarshalManifest([]byte(tt.data))
			if tt.wantErr {
				require.ErrorIs(t, err, controlplane.ErrDecode)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, "c", got.Channel)
		})
	}
}

func TestAnAliasedStringIsNotTurnedIntoATimestamp(t *testing.T) {
	t.Parallel()

	stamp := "2026-01-02T03:04:05Z"

	t.Run("the alias keeps its string in free-form positions", func(t *testing.T) {
		t.Parallel()

		data := "f:\n  metadata:\n    a: &d \"" + stamp + "\"\n    b: *d\n"
		got, _, err := controlplane.UnmarshalFlags([]byte(data), controlplane.FormatYAML)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"a": stamp, "b": stamp}, got["f"].Metadata)
	})

	t.Run("an anchored string is not a timestamp field", func(t *testing.T) {
		t.Parallel()

		data := "apiVersion: semaphore/v1\nkind: Manifest\ncreatedAt: &d \"" + stamp + "\"\n"
		_, _, err := controlplane.UnmarshalManifest([]byte(data))
		require.ErrorIs(t, err, controlplane.ErrDecode)
	})

	t.Run("a quoted string without an anchor is", func(t *testing.T) {
		t.Parallel()

		data := "apiVersion: semaphore/v1\nkind: Manifest\ncreatedAt: \"" + stamp + "\"\n"
		got, _, err := controlplane.UnmarshalManifest([]byte(data))
		require.NoError(t, err)
		assert.Equal(t, fixtureTime(2).UTC(), got.CreatedAt.UTC())
	})
}
