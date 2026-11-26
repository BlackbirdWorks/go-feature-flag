package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/cache"
)

const nullText = "null"

// flagJSON writes a one-flag JSON file with the given variations.
func flagJSON(variations string) string {
	return `{"x": {"variations": {` + variations + `}, "defaultRule": {"variation": "a"}}}`
}

// flagYAML writes a one-flag YAML file with the given variations.
func flagYAML(variations string) string {
	return "x:\n  variations:\n" + variations + "  defaultRule:\n    variation: a\n"
}

// A flags object is read by the parser of the engine, exactly as the version 1 relay reads it: for every form
// below, the object is invalid when the engine parser returns an error (the last good table is kept), and
// otherwise it serves the flags that the engine parser returns, converted the same way.
func TestFlagsObjectsAreParsedLikeTheEngineParser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format string
		bytes  string
	}{
		{name: "json blank", format: controlplane.FormatJSON, bytes: ""},
		{name: "json whitespace", format: controlplane.FormatJSON, bytes: " \n\t "},
		{name: "json null", format: controlplane.FormatJSON, bytes: nullText},
		{name: "json empty object", format: controlplane.FormatJSON, bytes: "{}"},
		{name: "json empty list", format: controlplane.FormatJSON, bytes: "[]"},
		{name: "json that is only a comment", format: controlplane.FormatJSON, bytes: "# nothing"},
		{name: "yaml blank", format: controlplane.FormatYAML, bytes: ""},
		{name: "yaml whitespace", format: controlplane.FormatYAML, bytes: " \n "},
		{name: "yaml null", format: controlplane.FormatYAML, bytes: nullText},
		{name: "yaml empty object", format: controlplane.FormatYAML, bytes: "{}"},
		{name: "yaml empty list", format: controlplane.FormatYAML, bytes: "[]"},
		{name: "yaml that is only a comment", format: controlplane.FormatYAML, bytes: "# nothing\n"},
		{name: "toml blank", format: controlplane.FormatTOML, bytes: ""},
		{name: "toml whitespace", format: controlplane.FormatTOML, bytes: "  \n"},
		{name: "toml that is only a comment", format: controlplane.FormatTOML, bytes: "# nothing\n"},
		{name: "toml null", format: controlplane.FormatTOML, bytes: nullText},
		{name: "toml empty object", format: controlplane.FormatTOML, bytes: "{}"},
		{name: "json integers above 2^53", format: controlplane.FormatJSON, bytes: flagJSON(`"a": 9007199254740993`)},
		{
			name: "json small and large numbers in one flag", format: controlplane.FormatJSON,
			bytes: flagJSON(`"a": 1, "b": 9007199254740993, "c": 1.5`),
		},
		{name: "json surrogate pair", format: controlplane.FormatJSON, bytes: flagJSON(`"a": "\uD83D\uDE00"`)},
		{name: "json lone high surrogate", format: controlplane.FormatJSON, bytes: flagJSON(`"a": "\uD83D"`)},
		{name: "json lone low surrogate", format: controlplane.FormatJSON, bytes: flagJSON(`"a": "\uDE00"`)},
		{
			name:   "json escaped backslash before letters",
			format: controlplane.FormatJSON,
			bytes:  flagJSON(`"a": "\\uD83D"`),
		},
		{name: "json duplicate variation names", format: controlplane.FormatJSON, bytes: flagJSON(`"a": 1, "a": 2`)},
		{
			name: "json duplicate flag names", format: controlplane.FormatJSON,
			bytes: `{"x": {"variations": {"a": 1}, "defaultRule": {"variation": "a"}}, ` +
				`"x": {"variations": {"a": 2}, "defaultRule": {"variation": "a"}}}`,
		},
		{
			name:   "yaml integers above 2^53",
			format: controlplane.FormatYAML,
			bytes:  flagYAML("    a: 9007199254740993\n"),
		},
		{name: "yaml surrogate pair", format: controlplane.FormatYAML, bytes: flagYAML("    a: \"\\uD83D\\uDE00\"\n")},
		{
			name:   "yaml duplicate variation names",
			format: controlplane.FormatYAML,
			bytes:  flagYAML("    a: 1\n    a: 2\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data := []byte(tt.bytes)
			engine, engineErr := cache.ConvertToFlagStruct(data, tt.format)

			builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
			base := objs{"f1": flagsObj(1, "kept"), setsName: setsObj(t, fset("a", src("f1"), keyA))}

			buildAndCommit(t, builder, content(1, base))

			object := flagruntime.Object{
				Kind:     flagruntime.KindFlags,
				Format:   tt.format,
				Bytes:    data,
				Checksum: controlplane.ChecksumOf(data).Value,
			}
			built := buildAndCommit(t, builder, content(2, base.with(objs{"f1": object})))
			served := built.Runtime.FlagSets["a"]

			if engineErr != nil {
				assert.Equal(t, flagruntime.ObjectInvalid, built.Runtime.Status.Objects["f1"].State)
				assert.Equal(t, []string{"kept"}, flagNames(served), "the last good table is kept")

				return
			}

			// a flag that the engine accepts is the flag that the engine converts; one that its validation refuses
			// is a rejected flag, never a different one
			assert.Equal(t, flagruntime.ObjectOK, built.Runtime.Status.Objects["f1"].State)

			accepted := 0

			for name, def := range engine {
				want := def.Convert()
				got, ok := served.Flag(name)

				if !ok {
					require.Error(
						t,
						want.IsValid(newService(t)),
						"flag %s is absent only if the engine refuses it",
						name,
					)

					continue
				}

				accepted++

				assert.Equal(t, &want, got, "flag %s", name)
			}

			assert.Len(t, served.Flags, accepted, "no flag besides the ones of the engine")
		})
	}
}

// The behaviours of the engine parser that a publisher can meet, observed and pinned: they are the "known limits"
// of the docs page.
func TestKnownLimitsOfTheEngineParser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantValue any
		name      string
		format    string
		bytes     string
	}{
		{
			name: "json rounds an integer above 2^53 to a float", format: controlplane.FormatJSON,
			bytes: flagJSON(`"a": 9007199254740993`), wantValue: float64(9007199254740992),
		},
		{
			name: "yaml keeps an integer above 2^53 exactly", format: controlplane.FormatYAML,
			bytes: flagYAML("    a: 9007199254740993\n"), wantValue: 9007199254740993,
		},
		{
			name: "json reads a surrogate pair as the character", format: controlplane.FormatJSON,
			bytes: flagJSON(`"a": "\uD83D\uDE00"`), wantValue: "\U0001F600",
		},
		{
			name: "json turns a lone surrogate into the replacement character", format: controlplane.FormatJSON,
			bytes: flagJSON(`"a": "\uD83D"`), wantValue: "\uFFFD",
		},
		{
			name: "json takes the later of two variations of the same name", format: controlplane.FormatJSON,
			bytes: flagJSON(`"a": 1, "a": 2`), wantValue: float64(2),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data := []byte(tt.bytes)
			object := flagruntime.Object{
				Kind:     flagruntime.KindFlags,
				Format:   tt.format,
				Bytes:    data,
				Checksum: controlplane.ChecksumOf(data).Value,
			}
			built := buildAndCommit(
				t,
				flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{}),
				content(1, objs{
					"f1": object, setsName: setsObj(t, fset("a", src("f1"), keyA)),
				}),
			)

			served, ok := built.Runtime.FlagSets["a"].Flag("x")

			require.True(t, ok)
			assert.Equal(t, tt.wantValue, served.GetVariationValue("a"))
		})
	}
}
