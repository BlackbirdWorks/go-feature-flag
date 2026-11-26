package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

const (
	nullText    = "null"
	commentOnly = "# nothing\n"
)

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
		{name: "yaml that is only a comment", format: controlplane.FormatYAML, bytes: commentOnly},
		{name: "toml blank", format: controlplane.FormatTOML, bytes: ""},
		{name: "toml whitespace", format: controlplane.FormatTOML, bytes: "  \n"},
		{name: "toml that is only a comment", format: controlplane.FormatTOML, bytes: commentOnly},
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

// knownLimit is one outcome of the engine parser that the docs page states. The expectations are fixed values, so
// a change of the parser that contradicts the docs fails here even when the runtime follows the parser.
type knownLimit struct {
	wantValue      any
	name           string
	format         string
	bytes          string
	wantState      flagruntime.ObjectState
	wantFlags      []string
	wantVariations []string
}

func knownLimits() []knownLimit {
	const (
		valid   = flagruntime.ObjectOK
		invalid = flagruntime.ObjectInvalid
	)

	jsonFmt, yamlFmt, tomlFmt := controlplane.FormatJSON, controlplane.FormatYAML, controlplane.FormatTOML
	one := []string{"x"}

	return []knownLimit{
		{name: "json blank", format: jsonFmt, bytes: "", wantState: invalid},
		{name: "json whitespace", format: jsonFmt, bytes: " \n\t ", wantState: invalid},
		{name: "json comment only", format: jsonFmt, bytes: "# nothing", wantState: invalid},
		{name: "json null", format: jsonFmt, bytes: nullText, wantState: valid},
		{name: "json empty object", format: jsonFmt, bytes: "{}", wantState: valid},
		{name: "json empty list", format: jsonFmt, bytes: "[]", wantState: invalid},
		{name: "yaml blank", format: yamlFmt, bytes: "", wantState: valid},
		{name: "yaml whitespace", format: yamlFmt, bytes: " \n ", wantState: valid},
		{name: "yaml comment only", format: yamlFmt, bytes: commentOnly, wantState: valid},
		{name: "yaml null", format: yamlFmt, bytes: nullText, wantState: valid},
		{name: "yaml empty object", format: yamlFmt, bytes: "{}", wantState: valid},
		{name: "yaml empty list", format: yamlFmt, bytes: "[]", wantState: invalid},
		{name: "toml blank", format: tomlFmt, bytes: "", wantState: valid},
		{name: "toml whitespace", format: tomlFmt, bytes: "  \n", wantState: valid},
		{name: "toml comment only", format: tomlFmt, bytes: commentOnly, wantState: valid},
		{name: "toml null", format: tomlFmt, bytes: nullText, wantState: invalid},
		{name: "toml empty object", format: tomlFmt, bytes: "{}", wantState: invalid},
		{name: "toml empty list", format: tomlFmt, bytes: "[]", wantState: invalid},
		{
			name:           "json rounds an integer above 2^53 to a float",
			format:         jsonFmt,
			bytes:          flagJSON(`"a": 9007199254740993`),
			wantState:      valid,
			wantFlags:      one,
			wantVariations: []string{"a"},
			wantValue:      float64(9007199254740992),
		},
		{
			name:           "yaml keeps an integer above 2^53 exactly",
			format:         yamlFmt,
			bytes:          flagYAML("    a: 9007199254740993\n"),
			wantState:      valid,
			wantFlags:      one,
			wantVariations: []string{"a"},
			wantValue:      9007199254740993,
		},
		{
			name: "json reads small and large numbers of one flag as floats", format: jsonFmt,
			bytes:     flagJSON(`"a": 1, "b": 9007199254740993, "c": 1.5`),
			wantState: valid, wantFlags: one, wantVariations: []string{"a", "b", "c"}, wantValue: float64(1),
		},
		{
			name:           "json reads a surrogate pair as the character",
			format:         jsonFmt,
			bytes:          flagJSON(`"a": "\uD83D\uDE00"`),
			wantState:      valid,
			wantFlags:      one,
			wantVariations: []string{"a"},
			wantValue:      "\U0001F600",
		},
		{
			name: "yaml rejects a surrogate pair escape", format: yamlFmt,
			bytes:     flagYAML("    a: \"\\uD83D\\uDE00\"\n"),
			wantState: invalid,
		},
		{
			name: "json turns a lone surrogate into the replacement character", format: jsonFmt,
			bytes:     flagJSON(`"a": "\uD83D"`),
			wantState: valid, wantFlags: one, wantVariations: []string{"a"}, wantValue: "\uFFFD",
		},
		{
			name:           "json takes the later of two variations of the same name",
			format:         jsonFmt,
			bytes:          flagJSON(`"a": 1, "a": 2`),
			wantState:      valid,
			wantFlags:      one,
			wantVariations: []string{"a"},
			wantValue:      float64(2),
		},
		{
			name: "json merges the entries of a repeated variations member", format: jsonFmt,
			bytes:     `{"x": {"variations": {"a": 1}, "variations": {"b": 2}, "defaultRule": {"variation": "a"}}}`,
			wantState: valid, wantFlags: one, wantVariations: []string{"a", "b"}, wantValue: float64(1),
		},
		{
			name: "json takes the later of two flags of the same name whole", format: jsonFmt,
			bytes: `{"x": {"variations": {"a": 1}, "defaultRule": {"variation": "a"}}, ` +
				`"x": {"variations": {"b": 2}, "defaultRule": {"variation": "b"}}}`,
			wantState: valid, wantFlags: one, wantVariations: []string{"b"},
		},
		{
			name:      "yaml rejects a repeated variation name",
			format:    yamlFmt,
			bytes:     flagYAML("    a: 1\n    a: 2\n"),
			wantState: invalid,
		},
		{
			name: "yaml rejects a repeated variations member", format: yamlFmt,
			bytes:     "x:\n  variations:\n    a: 1\n  variations:\n    b: 2\n  defaultRule:\n    variation: a\n",
			wantState: invalid,
		},
		{
			name: "yaml rejects a repeated flag name", format: yamlFmt,
			bytes: "x:\n  variations:\n    a: 1\n  defaultRule:\n    variation: a\n" +
				"x:\n  variations:\n    b: 2\n  defaultRule:\n    variation: b\n",
			wantState: invalid,
		},
	}
}

func TestKnownLimitsOfTheEngineParser(t *testing.T) {
	t.Parallel()

	for _, tt := range knownLimits() {
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

			assert.Equal(t, tt.wantState, built.Runtime.Status.Objects["f1"].State)
			assert.ElementsMatch(t, tt.wantFlags, flagNames(built.Runtime.FlagSets["a"]))

			served, ok := built.Runtime.FlagSets["a"].Flag("x")
			if tt.wantFlags == nil {
				assert.False(t, ok)

				return
			}

			require.True(t, ok)
			assert.ElementsMatch(t, tt.wantVariations, variationNames(served))
			assert.Equal(t, tt.wantValue, served.GetVariationValue("a"))
		})
	}
}

func variationNames(flag *flagdef.InternalFlag) []string {
	names := make([]string, 0, len(flag.GetVariations()))
	for name := range flag.GetVariations() {
		names = append(names, name)
	}

	return names
}
