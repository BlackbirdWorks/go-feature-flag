package controlplane_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

const manifestHead = "apiVersion: semaphore/v1\nkind: Manifest\nchannel: stable\n"

func TestDecodeRulesOfBothFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		data        string
		wantReason  string
		wantErrLine bool
	}{
		{
			"fraction in an integer field",
			manifestHead + "generation: 1.5\nobjects: []\n",
			"generation: expected an unsigned integer",
			true,
		},
		{
			"overflow of uint64",
			manifestHead + "generation: 18446744073709551616\nobjects: []\n",
			"generation: integer out of range",
			true,
		},
		{
			"negative in an unsigned field",
			manifestHead + "generation: -1\nobjects: []\n",
			"generation: integer out of range",
			true,
		},
		{
			"string in an integer field",
			manifestHead + "generation: \"3\"\nobjects: []\n",
			"generation: expected an unsigned integer",
			true,
		},
		{
			"fraction in size",
			manifestHead + "generation: 1\nobjects:\n  - name: a\n    size: 1.5\n",
			"objects[0].size: expected an integer",
			true,
		},
		{
			"overflow in size",
			manifestHead + "generation: 1\nobjects:\n  - name: a\n    size: 9223372036854775808\n",
			"objects[0].size: integer out of range",
			true,
		},
		{
			"fraction in version",
			manifestHead + "generation: 1\nobjects:\n  - name: a\n    version: 1.0\n",
			"objects[0].version: expected an integer",
			true,
		},
		{"scalar for a list", manifestHead + "generation: 1\nobjects: nothing\n", "objects: expected a list", true},
		{
			"two yaml documents",
			manifestHead + "generation: 1\nobjects: []\n---\nchannel: other\n",
			"only one YAML document",
			true,
		},
		{
			"duplicate key",
			manifestHead + "channel: other\ngeneration: 1\nobjects: []\n",
			"channel: duplicate key",
			true,
		},
		{
			"duplicate key in json",
			`{"apiVersion":"semaphore/v1","kind":"Manifest","generation":1,"generation":2}`,
			"generation: duplicate key",
			false,
		},
		{"non-string key", manifestHead + "generation: 1\nobjects: []\n1: x\n", "keys must be strings", true},
		{"null key", manifestHead + "generation: 1\nobjects: []\n~: x\n", "keys must be strings", true},
		{
			"data after the json value",
			`{"apiVersion":"semaphore/v1","kind":"Manifest"} {}`,
			"only one YAML document",
			false,
		},
		{"syntax problem", manifestHead + "generation: [\n", "syntax error", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := controlplane.UnmarshalManifest([]byte(tt.data))
			require.Error(t, err)

			require.ErrorIs(t, err, controlplane.ErrDecode)

			assert.Contains(t, err.Error(), tt.wantReason)

			if tt.wantErrLine {
				assert.Contains(t, err.Error(), "(line ")
			}
		})
	}
}

func TestJSONIsReadLikeYAML(t *testing.T) {
	t.Parallel()

	t.Run("field names are case sensitive", func(t *testing.T) {
		t.Parallel()

		data := `{"apiVersion":"semaphore/v1","kind":"Manifest",` +
			`"Channel":"upper","channel":"lower","Generation":9,"generation":2}`
		got, info, err := controlplane.UnmarshalManifest([]byte(data))
		require.NoError(t, err)
		assert.Equal(t, "lower", got.Channel)
		assert.Equal(t, uint64(2), got.Generation)
		assert.Equal(t, []string{"Channel", "Generation"}, info.UnknownFields)
	})

	t.Run("a json string is never a number or a bool", func(t *testing.T) {
		t.Parallel()

		got, _, err := controlplane.UnmarshalFlags(
			[]byte(`{"f":{"metadata":{"a":"007","b":"yes","c":"null","d":"1e3"}}}`),
			controlplane.FormatJSON,
		)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"a": "007", "b": textYes, "c": textNull, "d": textExp}, got["f"].Metadata)
	})

	t.Run("a json string in an integer field is rejected", func(t *testing.T) {
		t.Parallel()

		_, _, err := controlplane.UnmarshalManifest(
			[]byte(`{"apiVersion":"semaphore/v1","kind":"Manifest","generation":"3"}`),
		)
		require.ErrorIs(t, err, controlplane.ErrDecode)
	})

	t.Run("timestamps are read from json strings", func(t *testing.T) {
		t.Parallel()

		got, _, err := controlplane.UnmarshalManifest(
			[]byte(`{"apiVersion":"semaphore/v1","kind":"Manifest","createdAt":"2026-01-02T03:04:05Z"}`),
		)
		require.NoError(t, err)
		assert.Equal(t, fixtureTime(2).UTC(), got.CreatedAt.UTC())
	})

	t.Run("a bad json timestamp is rejected", func(t *testing.T) {
		t.Parallel()

		_, _, err := controlplane.UnmarshalManifest(
			[]byte(`{"apiVersion":"semaphore/v1","kind":"Manifest","createdAt":"yesterday"}`),
		)
		require.ErrorIs(t, err, controlplane.ErrDecode)
	})

	t.Run("flow style yaml with bare keys is yaml, not json", func(t *testing.T) {
		t.Parallel()

		got, _, err := controlplane.UnmarshalManifest(
			[]byte("{apiVersion: semaphore/v1, kind: Manifest, channel: flow, generation: 3}"),
		)
		require.NoError(t, err)
		assert.Equal(t, "flow", got.Channel)
	})

	t.Run("format json requires json", func(t *testing.T) {
		t.Parallel()

		_, _, err := controlplane.UnmarshalFlags([]byte("f: {}"), controlplane.FormatJSON)
		require.ErrorIs(t, err, controlplane.ErrDecode)
	})

	t.Run("anchors, aliases and merge keys are read", func(t *testing.T) {
		t.Parallel()

		data := "base: &b\n  variations: {a: 1, b: 2}\n  defaultRule: {variation: a}\ncopy:\n  <<: *b\n  version: \"2\"\n"
		got, _, err := controlplane.UnmarshalFlags([]byte(data), controlplane.FormatYAML)
		require.NoError(t, err)
		assert.Equal(t, got["base"].Variations, got["copy"].Variations)
		assert.Equal(t, "2", *got["copy"].Version)

		written, err := controlplane.MarshalFlags(got)
		require.NoError(t, err)

		for _, forbidden := range []string{"&", "*", "<<"} {
			assert.NotContains(t, string(written), forbidden, "never written")
		}
	})

	t.Run("an alias bomb is refused", func(t *testing.T) {
		t.Parallel()

		var doc strings.Builder

		doc.WriteString("a: &a [a1, a2, a3, a4, a5, a6, a7, a8, a9, a0]\n")

		for i, prev := 'b', 'a'; i < 'k'; i, prev = i+1, i {
			doc.WriteString(string(i) + ": &" + string(i) + " [")

			for range 10 {
				doc.WriteString("*" + string(prev) + ",")
			}

			doc.WriteString("]\n")
		}

		doc.WriteString("f: {metadata: {big: *j}}\n")

		_, _, err := controlplane.UnmarshalFlags([]byte(doc.String()), controlplane.FormatYAML)
		require.Error(t, err)
	})
}

// TestErrorsNeverQuoteValues feeds malformed documents whose values are canaries and asserts that no canary
// appears in any error.
// decoderUnderTest names the decoder that a malformed document is aimed at.
type decoderUnderTest int

const (
	decodeFlagSet decoderUnderTest = iota
	decodeManifest
	decodeFlags
)

// decodeWith runs one decoder and returns its error.
func decodeWith(which decoderUnderTest, data string) error {
	var err error

	switch which {
	case decodeFlagSet:
		_, _, err = controlplane.UnmarshalFlagSet([]byte(data))
	case decodeManifest:
		_, _, err = controlplane.UnmarshalManifest([]byte(data))
	case decodeFlags:
		_, _, err = controlplane.UnmarshalFlags([]byte(data), controlplane.FormatYAML)
	}

	return err
}

// TestErrorsNeverQuoteValues feeds malformed documents whose values are canaries to the decoder each one is aimed
// at. That decoder must refuse the document with a decode error, and the error must not contain the canary.
func TestErrorsNeverQuoteValues(t *testing.T) {
	t.Parallel()

	const canary = "sekret-1234"

	setHead := "apiVersion: semaphore/v1\nkind: FlagSet\nflagSets:\n  - name: checkout\n    sources: [a]\n"
	manifestHead := "apiVersion: semaphore/v1\nkind: Manifest\n"

	tests := []struct {
		name        string
		data        string
		wantDecoder decoderUnderTest
	}{
		{"keys as a scalar", setHead + "    apiKeys: " + canary + "\n", decodeFlagSet},
		{"keys as a number", setHead + "    apiKeys: 12345678\n", decodeFlagSet},
		{"keys as a nested map", setHead + "    apiKeys: {" + canary + ": 1}\n", decodeFlagSet},
		{"keys with a nested list", setHead + "    apiKeys: [[" + canary + "]]\n", decodeFlagSet},
		{"keys as a block map", setHead + "    apiKeys:\n      " + canary + ": 12345678\n", decodeFlagSet},
		{
			"name as a list",
			"apiVersion: semaphore/v1\nkind: FlagSet\nflagSets:\n  - name: [" + canary + "]\n",
			decodeFlagSet,
		},
		{
			"duplicate key holding the canary",
			setHead + "    apiKeys: [x]\n    apiKeys: [" + canary + "]\n",
			decodeFlagSet,
		},
		{"syntax error around the canary", setHead + "    apiKeys: [" + canary + ", {\n", decodeFlagSet},
		{"bad anchor use", setHead + "    apiKeys: *" + canary + "\n", decodeFlagSet},
		{"tab indentation", setHead + "\tapiKeys: [" + canary + "]\n", decodeFlagSet},
		{"unterminated quote", setHead + "    apiKeys: [\"" + canary + "]\n", decodeFlagSet},
		{"second document", setHead + "    apiKeys: [" + canary + "]\n---\n" + canary + ": 1\n", decodeFlagSet},
		{
			"json keys as a string",
			`{"apiVersion":"semaphore/v1","kind":"FlagSet",` +
				`"flagSets":[{"name":"c","sources":["a"],"apiKeys":"` + canary + `"}]}`,
			decodeFlagSet,
		},
		{"json trailing data", `{"apiVersion":"semaphore/v1","kind":"FlagSet"} ` + canary, decodeFlagSet},
		{"unsupported tag", setHead + "    apiKeys: [!!binary " + canary + "]\n", decodeFlagSet},
		{"manifest generation as text", manifestHead + "generation: " + canary + "\n", decodeManifest},
		{"manifest objects as text", manifestHead + "objects: " + canary + "\n", decodeManifest},
		{"manifest checksum as a list", manifestHead + "objects:\n  - checksum: [" + canary + "]\n", decodeManifest},
		{"manifest duplicate key", manifestHead + "channel: x\nchannel: " + canary + "\n", decodeManifest},
		{"flag as text", "f: " + canary + "\n", decodeFlags},
		{"flag variations as text", "f:\n  variations: " + canary + "\n", decodeFlags},
		{"flag rule as a list", "f:\n  defaultRule: [" + canary + "]\n", decodeFlags},
		{"flags syntax error", "f: [" + canary + ", {\n", decodeFlags},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := decodeWith(tt.wantDecoder, tt.data)

			require.ErrorIs(t, err, controlplane.ErrDecode, "the aimed decoder must refuse the document")
			assert.NotContains(t, err.Error(), canary)
			assert.NotContains(t, err.Error(), "12345678")
		})
	}
}

// TestHeaderValuesInErrorsAreShort: the apiVersion and kind a document carries are named in the error (the
// caller needs them), but a long value is cut.
func TestHeaderValuesInErrorsAreShort(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("v", 500)

	_, _, versionErr := controlplane.UnmarshalManifest([]byte("apiVersion: " + long + "\nkind: Manifest\n"))
	_, _, kindErr := controlplane.UnmarshalManifest([]byte("apiVersion: semaphore/v1\nkind: " + long + "\n"))

	require.ErrorIs(t, versionErr, controlplane.ErrUnsupportedAPIVersion)
	require.ErrorIs(t, kindErr, controlplane.ErrWrongKind)
	assert.Less(t, len(versionErr.Error()), 200)
	assert.Less(t, len(kindErr.Error()), 200)
}
