package controlplane_test

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// stringCorpus is a list of strings that a naive writer corrupts: control characters, edge whitespace, YAML
// syntax, unicode separators, long lines.
func stringCorpus() []string {
	return []string{
		"\n",
		"\n\nx",
		"x\n",
		"\ta\n\tb",
		"\t",
		" lead",
		"trail ",
		"  both  ",
		"\r\n",
		"a\rb",
		"a\u2028b",
		"a\u2029b",
		"\x00",
		"a\x00b",
		"\x1b[0m",
		"\u0085",
		"\ufeffbom",
		"a\u00a0b",
		"\u00a0",
		"emoji \U0001F600",
		"caf\u00e9",
		`"quoted"`,
		`'single'`,
		"a: b",
		"- x",
		"# comment",
		"a #b",
		"&anchor",
		"*alias",
		"!tag",
		"|",
		">",
		"%x",
		"@x",
		"`x`",
		"{}",
		"[]",
		"{a: b}",
		"[a, b]",
		"? x",
		": x",
		"key: value\nother: 2",
		`back\slash`,
		`trailing\`,
		"a  b",
		"a   b  c",
		strings.Repeat("word ", 80),
		strings.Repeat("x", 400),
		strings.Repeat("a  b ", 60),
		"line1\nline2\n",
		"\n\n\n",
		"- a\n- b",
		"---",
		"...",
		"--- x",
		textYes,
		"no",
		"on",
		variationOff,
		"y",
		"n",
		"007",
		textExp,
		textDate,
		"2026-01-01T00:00:00Z",
		textNull,
		"~",
		"<<",
		"=",
		".inf",
		"-.5",
		"+1",
		"0x1f",
		"0o17",
		"1_000",
		"1:30",
		textTrue,
		"",
	}
}

// stringsFlags puts a string in every position of a flag file that holds text.
func stringsFlags(text string) controlplane.Flags {
	return controlplane.Flags{
		text: {
			Version:      new(text),
			Variations:   map[string]any{text: text, "other": text},
			Targeting:    []controlplane.Rule{{Name: new(text), Query: new(text), Variation: new(text)}},
			BucketingKey: new(text),
			DefaultRule:  &controlplane.Rule{Variation: new(text)},
			Metadata:     map[string]any{text: text, "list": []any{text, map[string]any{text: text}}},
		},
	}
}

func TestStringsRoundTripEveryPosition(t *testing.T) {
	t.Parallel()

	for _, text := range stringCorpus() {
		t.Run(strings.ToValidUTF8(text, "?")[:min(len(text), 12)], func(t *testing.T) {
			t.Parallel()

			flags := stringsFlags(text)
			set := &controlplane.FlagSetDocument{
				FlagSets: []controlplane.FlagSet{{Name: text, Sources: []string{text}, APIKeys: []string{text}}},
			}
			manifest := &controlplane.Manifest{
				Channel:    text,
				Generation: 1,
				Objects: []controlplane.ManifestObject{
					{Name: text, Kind: text, Connection: text, Key: text, Format: text, VersionID: text},
				},
			}

			for _, asJSON := range []bool{false, true} {
				writeFlags, writeSet := controlplane.MarshalFlags, controlplane.MarshalFlagSet
				writeManifest := controlplane.MarshalManifest
				format := controlplane.FormatYAML

				if asJSON {
					writeFlags, writeSet = controlplane.MarshalFlagsJSON, controlplane.MarshalFlagSetJSON
					writeManifest = controlplane.MarshalManifestJSON
					format = controlplane.FormatJSON
				}

				data, err := writeFlags(flags)
				require.NoError(t, err)

				gotFlags, info, err := controlplane.UnmarshalFlags(data, format)
				require.NoError(t, err, "%s", data)
				assert.Empty(t, info.UnknownFields)
				assert.Equal(t, flags, gotFlags, "%s", data)

				data, err = writeSet(set)
				require.NoError(t, err)

				gotSet, _, err := controlplane.UnmarshalFlagSet(data)
				require.NoError(t, err, "%s", data)
				assert.Equal(
					t,
					controlplane.FlagSet{Name: text, Sources: []string{text}, APIKeys: []string{text}},
					gotSet.FlagSets[0],
				)

				data, err = writeManifest(manifest)
				require.NoError(t, err)

				gotManifest, _, err := controlplane.UnmarshalManifest(data)
				require.NoError(t, err, "%s", data)
				assert.Equal(t, manifest.Objects, gotManifest.Objects)
				assert.Equal(t, text, gotManifest.Channel)
			}
		})
	}
}

func TestMarshalRejectsUnwritableValues(t *testing.T) {
	t.Parallel()

	withMeta := func(value any) controlplane.Flags {
		return controlplane.Flags{"f": {Metadata: map[string]any{"k": value}}}
	}

	tests := []struct {
		flags controlplane.Flags
		name  string
	}{
		{withMeta(math.NaN()), "NaN"},
		{withMeta(math.Inf(1)), "positive infinity"},
		{withMeta(math.Inf(-1)), "negative infinity"},
		{withMeta(map[int]string{1: "x"}), "map with integer keys"},
		{withMeta(map[any]any{"a": 1}), "map of any keys"},
		{withMeta("bad \xff utf8"), "invalid UTF-8 string"},
		{withMeta(make(chan int)), "channel"},
		{controlplane.Flags{"bad \xff key": {}}, "invalid UTF-8 key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, yamlErr := controlplane.MarshalFlags(tt.flags)
			_, jsonErr := controlplane.MarshalFlagsJSON(tt.flags)

			require.ErrorIs(t, yamlErr, controlplane.ErrUnsupportedValue)
			require.ErrorIs(t, jsonErr, controlplane.ErrUnsupportedValue)
			assert.Contains(t, yamlErr.Error(), "unsupported value")
		})
	}
}
