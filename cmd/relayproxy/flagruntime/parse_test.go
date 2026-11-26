package flagruntime_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

const (
	nullText  = "null"
	keptValue = "kept"
)

const oneFlagJSON = `{"x": {"variations": {"v": "\uD83D\uDE00"}, "defaultRule": {"variation": "v"}}}`

// What a flags object that declares a format may contain: a table of the empty and the odd forms. The outcome is
// the one of the loader of the relay before the runtime existed: a form that it read as "no flag" is a table
// with no flag, a form that it rejected keeps the last good table of the object.
func TestFlagsObjectForms(t *testing.T) {
	t.Parallel()

	const (
		valid   = flagruntime.ObjectOK
		invalid = flagruntime.ObjectInvalid
	)

	tests := []struct {
		name      string
		format    string
		bytes     string
		wantState flagruntime.ObjectState
	}{
		{name: "json blank", format: controlplane.FormatJSON, bytes: "", wantState: invalid},
		{name: "json whitespace", format: controlplane.FormatJSON, bytes: " \n\t ", wantState: invalid},
		{name: "json null", format: controlplane.FormatJSON, bytes: nullText, wantState: valid},
		{name: "json empty object", format: controlplane.FormatJSON, bytes: "{}", wantState: valid},
		{name: "json empty list", format: controlplane.FormatJSON, bytes: "[]", wantState: invalid},
		{name: "json that is only a comment", format: controlplane.FormatJSON, bytes: "# nothing", wantState: invalid},
		{name: "yaml blank", format: controlplane.FormatYAML, bytes: "", wantState: valid},
		{name: "yaml whitespace", format: controlplane.FormatYAML, bytes: " \n ", wantState: valid},
		{name: "yaml null", format: controlplane.FormatYAML, bytes: nullText, wantState: valid},
		{name: "yaml empty object", format: controlplane.FormatYAML, bytes: "{}", wantState: valid},
		{name: "yaml empty list", format: controlplane.FormatYAML, bytes: "[]", wantState: invalid},
		{name: "yaml that is only a comment", format: controlplane.FormatYAML, bytes: "# nothing\n", wantState: valid},
		{name: "toml blank", format: controlplane.FormatTOML, bytes: "", wantState: valid},
		{name: "toml whitespace", format: controlplane.FormatTOML, bytes: "  \n", wantState: valid},
		{name: "toml that is only a comment", format: controlplane.FormatTOML, bytes: "# nothing\n", wantState: valid},
		{name: "toml null", format: controlplane.FormatTOML, bytes: nullText, wantState: invalid},
		{name: "toml empty object", format: controlplane.FormatTOML, bytes: "{}", wantState: invalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
			base := objs{"f1": flagsObj(1, "x"), setsName: setsObj(t, fset("a", src("f1"), keyA))}

			buildAndCommit(t, builder, content(1, base))

			data := []byte(tt.bytes)
			object := flagruntime.Object{
				Kind:     flagruntime.KindFlags,
				Format:   tt.format,
				Bytes:    data,
				Checksum: controlplane.ChecksumOf(data).Value,
			}
			built := buildAndCommit(t, builder, content(2, base.with(objs{"f1": object})))

			assert.Equal(t, tt.wantState, built.Runtime.Status.Objects["f1"].State)

			wantFlags := []string{}
			if tt.wantState == flagruntime.ObjectInvalid {
				wantFlags = []string{"x"} // the last good table is kept
			}

			assert.ElementsMatch(t, wantFlags, flagNames(built.Runtime.FlagSets["a"]))
		})
	}
}

// A JSON escape of a character outside the basic plane (a surrogate pair) is read as that character. A lone
// surrogate is a decode error that keeps the last good table: it does not corrupt a value.
func TestJSONEscapesOfSurrogates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantValue any
		name      string
		bytes     string
		wantState flagruntime.ObjectState
	}{
		{
			name: "a surrogate pair", bytes: oneFlagJSON, wantState: flagruntime.ObjectOK, wantValue: "\U0001F600",
		},
		{
			name:      "a lone high surrogate",
			bytes:     `{"x": {"variations": {"v": "\uD83D"}, "defaultRule": {"variation": "v"}}}`,
			wantState: flagruntime.ObjectInvalid, wantValue: keptValue,
		},
		{
			name:      "a lone low surrogate",
			bytes:     `{"x": {"variations": {"v": "\uDE00"}, "defaultRule": {"variation": "v"}}}`,
			wantState: flagruntime.ObjectInvalid, wantValue: keptValue,
		},
		{
			name:      "a high surrogate followed by another escape",
			bytes:     `{"x": {"variations": {"v": "\uD83D\u0041"}, "defaultRule": {"variation": "v"}}}`,
			wantState: flagruntime.ObjectInvalid, wantValue: keptValue,
		},
		{
			name:      "an escaped backslash before the letters is text, not an escape",
			bytes:     `{"x": {"variations": {"v": "\\uD83D"}, "defaultRule": {"variation": "v"}}}`,
			wantState: flagruntime.ObjectOK, wantValue: `\uD83D`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
			good := obj(flagruntime.KindFlags, []byte(
				"x:\n  variations:\n    v: "+keptValue+"\n  defaultRule:\n    variation: v\n"))
			base := objs{"f1": good, setsName: setsObj(t, fset("a", src("f1"), keyA))}

			buildAndCommit(t, builder, content(1, base))

			data := []byte(tt.bytes)
			published := flagruntime.Object{
				Kind: flagruntime.KindFlags, Format: controlplane.FormatJSON, Bytes: data,
				Checksum: controlplane.ChecksumOf(data).Value,
			}
			built := buildAndCommit(t, builder, content(2, base.with(objs{"f1": published})))

			assert.Equal(t, tt.wantState, built.Runtime.Status.Objects["f1"].State)
			assert.Equal(t, tt.wantValue, value(built.Runtime.FlagSets["a"], "x"))
		})
	}
}

func TestLargeJSONIntegersAreKeptExactly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		first  int64
		second int64
	}{
		{name: "the next odd integers above 2^53", first: 9007199254740993, second: 9007199254740995},
		{
			// both round to the same float64: the second is only seen as a change if the integers are exact
			name: "two integers that round to the same float", first: 18014398509481985, second: 18014398509481986,
		},
	}

	jsonFlags := func(value int64) flagruntime.Object {
		data := fmt.Appendf(nil, `{"x": {"variations": {"v": %d}, "defaultRule": {"variation": "v"}}}`, value)

		return flagruntime.Object{
			Kind: flagruntime.KindFlags, Format: controlplane.FormatJSON, Bytes: data,
			Checksum: controlplane.ChecksumOf(data).Value,
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			harness := newRig(t, flagruntime.Hooks{})
			harness.start()

			sets := setsObj(t, fset("a", src("f1"), keyA))

			harness.feed(content(1, objs{"f1": jsonFlags(tt.first), setsName: sets}))
			harness.nextChange()

			assert.EqualValues(t, tt.first, value(harness.holder.Load().FlagSets["a"], "x"))

			harness.feed(content(2, objs{"f1": jsonFlags(tt.second), setsName: sets}))

			update, ok := harness.nextChange().Diff.Updated["x"]

			require.True(t, ok, "the next integer is a change, not the same rounded float")
			assert.EqualValues(t, tt.first, update.Before.GetVariationValue("v"))
			assert.EqualValues(t, tt.second, update.After.GetVariationValue("v"))
		})
	}
}
