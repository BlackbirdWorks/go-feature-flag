package controlplane_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// yaml11Type returns the type a YAML 1.1 reader gives to the plain scalar text.
func yaml11Type(text string) string {
	patterns := map[string]*regexp.Regexp{
		"bool": regexp.MustCompile(
			`^(y|Y|yes|Yes|YES|n|N|no|No|NO|true|True|TRUE|false|False|FALSE|on|On|ON|off|Off|OFF)$`,
		),
		textNull: regexp.MustCompile(`^(~|null|Null|NULL|)$`),
		keyInt: regexp.MustCompile(
			`^[-+]?(0b[0-1_]+|0[0-7_]+|0x[0-9a-fA-F_]+|[0-9][0-9_]*|[1-9][0-9_]*(:[0-5]?[0-9])+)$`,
		),
		"decimal": regexp.MustCompile(
			`^([-+]?([0-9][0-9_]*)?\.[0-9.]*([eE][-+][0-9]+)?|[-+]?[0-9][0-9_]*(:[0-5]?[0-9])+\.[0-9_]*|` +
				`[-+]?\.(inf|Inf|INF)|\.(nan|NaN|NAN))$`,
		),
		"float-exp": regexp.MustCompile(`^[-+]?\d[0-9_]*([eE][-+]?\d+)$`),
		"timestamp": regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([Tt ].*)?$`),
		"merge":     regexp.MustCompile(`^(<<|=)$`),
	}

	for typ, re := range patterns {
		if re.MatchString(text) {
			return typ
		}
	}

	return "str"
}

// stringScalars walks a parsed document and returns every scalar node.
func scalars(node *yaml.Node) []*yaml.Node {
	var out []*yaml.Node
	if node.Kind == yaml.ScalarNode {
		out = append(out, node)
	}

	for _, child := range node.Content {
		out = append(out, scalars(child)...)
	}

	return out
}

func TestScalarQuoting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
	}{
		{textYes, textYes}, {"Yes", "Yes"}, {"YES", "YES"}, {"no", "no"}, {"No", "No"},
		{"on", "on"}, {"On", "On"}, {variationOff, variationOff}, {"Off", "Off"},
		{"y", "y"}, {"Y", "Y"}, {"n", "n"}, {"N", "N"},
		{"true", textTrue}, {"False", "False"},
		{"leading zeros", "007"}, {"exponent", textExp}, {"integer", "42"}, {keyFloat, "1.5"},
		{"hex", "0x1F"}, {"octal", "0o17"}, {"sexagesimal", "1:30"}, {"underscore", "1_000"},
		{"date", textDate}, {"datetime", "2026-01-01T10:00:00Z"},
		{textNull, textNull}, {"Null", "Null"}, {"tilde", "~"}, {"empty string", ""},
		{"nan", ".nan"}, {"inf", "-.inf"}, {"signed", "+5"}, {"merge", "<<"},
		{"plain", "hello world"}, {"colon", "a: b"}, {"hash", "a #b"}, {"multi line", "a\nb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flags := controlplane.Flags{tt.value + "x": {
				Variations:  map[string]any{tt.value: tt.value, "v": tt.value},
				DefaultRule: &controlplane.Rule{Variation: new("v")},
			}, tt.value: {Variations: map[string]any{"v": tt.value}}}

			b, err := controlplane.MarshalFlags(flags)
			require.NoError(t, err)

			// YAML 1.2 reading (yaml.v3): decoded into any, every value and key is the original string.
			var decoded map[string]map[string]any

			require.NoError(t, yaml.Unmarshal(b, &decoded))

			variations, ok := decoded[tt.value]["variations"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tt.value, variations["v"])

			// Both readings agree on every scalar that holds the string: the node is either quoted (a
			// string in both) or a plain scalar that is a string under 1.2 and under 1.1.
			var doc yaml.Node

			require.NoError(t, yaml.Unmarshal(b, &doc))

			quotedStyles := yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle | yaml.LiteralStyle | yaml.FoldedStyle

			matched := 0

			for _, scalar := range scalars(&doc) {
				if scalar.Value != tt.value && scalar.Value != tt.value+"x" {
					continue
				}

				matched++

				assert.Equal(t, "!!str", scalar.Tag, "1.2 reading of %q in\n%s", scalar.Value, b)

				if scalar.Style&quotedStyles == 0 {
					assert.Equal(t, "str", yaml11Type(scalar.Value), "1.1 reading of plain %q in\n%s", scalar.Value, b)
				}
			}

			assert.Positive(t, matched, "the string must appear in the output")
			assert.NotContains(t, string(b), "&", "no anchors")
		})
	}
}
