package schemadoc_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/schemadoc"
)

const (
	typeInteger    = "integer"
	repoRoot       = "../.."
	bootstrapPath  = repoRoot + "/.schema/bootstrap-schema.json"
	referencePath  = repoRoot + "/docs/reference/schema.md"
	bootstrapPage  = repoRoot + "/docs/relay-proxy/bootstrap-config.md"
	keysSectionEnd = "## Full example"
)

func schemaPath(file string) string { return repoRoot + "/.schema/" + file }

func read(t *testing.T, path string) []byte {
	t.Helper()

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	return raw
}

// The committed schemas of the control-plane documents and the committed reference are what the generator writes.
// Regenerate them with `task docs-schema`.
func TestGeneratedFilesAreCurrent(t *testing.T) {
	t.Parallel()

	controlPlane, err := schemadoc.ControlPlaneSchemas(repoRoot)
	require.NoError(t, err)

	reference, err := schemadoc.Render(schemadoc.Documents(read(t, bootstrapPath), controlPlane))
	require.NoError(t, err)

	tests := []struct {
		name string
		path string
		want []byte
	}{
		{
			name: "manifest",
			path: schemaPath(schemadoc.ManifestSchemaFile),
			want: controlPlane[schemadoc.ManifestSchemaFile],
		},
		{
			name: "flag set",
			path: schemaPath(schemadoc.FlagSetSchemaFile),
			want: controlPlane[schemadoc.FlagSetSchemaFile],
		},
		{
			name: "segments",
			path: schemaPath(schemadoc.SegmentsSchemaFile),
			want: controlPlane[schemadoc.SegmentsSchemaFile],
		},
		{name: "reference", path: referencePath, want: reference},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, string(tt.want), string(read(t, tt.path)), "stale file: run `task docs-schema`")
		})
	}
}

const sampleSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$ref": "#/$defs/Root",
  "$defs": {
    "Duration": {"type": "string", "pattern": "^[0-9]+s$"},
    "Item": {
      "type": "object",
      "required": ["name"],
      "properties": {"name": {"type": "string"}, "size": {"type": "integer", "minimum": 1, "default": 4}}
    },
    "Root": {
      "type": "object",
      "required": ["version"],
      "properties": {
        "version": {"type": "integer", "enum": [2]},
        "wait": {"$ref": "#/$defs/Duration", "default": "5s"},
        "items": {"type": "array", "items": {"$ref": "#/$defs/Item"}},
        "named": {"type": "object", "additionalProperties": {"$ref": "#/$defs/Item"}},
        "labels": {"type": "object", "additionalProperties": {"type": "string"}}
      }
    }
  }
}`

func TestKeys(t *testing.T) {
	t.Parallel()

	keys, err := schemadoc.Keys([]byte(sampleSchema))
	require.NoError(t, err)

	type row struct{ path, kind, def, allowed string }

	got := make([]row, 0, len(keys))
	for _, key := range keys {
		got = append(got, row{key.Path, key.Type, key.Default, key.Allowed})
	}

	want := []row{
		{"items", "list of object", "", ""},
		{"items[].name", "string", "", ""},
		{"items[].size", typeInteger, "4", "at least 1"},
		{"labels", "map of string", "", ""},
		{"named", "map of object", "", ""},
		{"named.<name>.name", "string", "", ""},
		{"named.<name>.size", typeInteger, "4", "at least 1"},
		{"version", typeInteger, "", "one of `2`"},
		{"wait", "duration", "5s", ""},
	}

	assert.Equal(t, want, got)
	assert.True(t, slices.ContainsFunc(keys, func(k schemadoc.Key) bool { return k.Path == "version" && k.Required }))
}

func TestRender(t *testing.T) {
	t.Parallel()

	got, err := schemadoc.Render(
		[]schemadoc.Document{{Title: "Sample", Intro: "A sample.", Schema: []byte(sampleSchema)}},
	)
	require.NoError(t, err)

	text := string(got)
	assert.Contains(
		t,
		text,
		"\n## Sample\n\nA sample.\n\n| Key | Type | Required | Default | Allowed | Description |\n",
	)
	assert.Contains(t, text, "| `items[].size` | integer |  | `4` | at least 1 |  |\n")
	assert.Contains(t, text, "| `version` | integer | yes |  | one of `2` |  |\n")
	assert.Contains(t, text, "| `wait` | duration |  | `5s` |  |  |\n")
}

var (
	backtickSpan = regexp.MustCompile("`([^`]+)`")
	headingKey   = regexp.MustCompile("^#{2,4} `([^`]+)`")
)

// pageKeys returns the keys the page documents, with the prefix of the section heading applied to the keys that are
// relative: the first column named Key of each table of the section.
func pageKeys(page string) []string {
	var out []string

	prefix := ""
	column := -1

	section, _, _ := strings.Cut(page, keysSectionEnd)
	_, section, _ = strings.Cut(section, "\n## Keys and defaults")

	for line := range strings.SplitSeq(section, "\n") {
		if strings.HasPrefix(line, "#") {
			prefix, column = "", -1

			if match := headingKey.FindStringSubmatch(line); match != nil {
				prefix = match[1]
			}

			continue
		}

		if !strings.HasPrefix(line, "|") {
			column = -1

			continue
		}

		cells := strings.Split(strings.Trim(line, "| "), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}

		if idx := slices.Index(cells, "Key"); idx >= 0 {
			column = idx

			continue
		}

		if column < 0 || column >= len(cells) || strings.HasPrefix(cells[0], "---") {
			continue
		}

		for _, match := range backtickSpan.FindAllStringSubmatch(cells[column], -1) {
			out = append(out, prefix+"\x00"+match[1])
		}
	}

	return out
}

// Every key of the bootstrap schema is on the bootstrap reference page, and every key of the page is in the schema.
// An object that has documented keys below it needs no row of its own.
func TestBootstrapPageListsEveryKey(t *testing.T) {
	t.Parallel()

	keys, err := schemadoc.Keys(read(t, bootstrapPath))
	require.NoError(t, err)

	schemaPaths := map[string]bool{}
	for _, key := range keys {
		schemaPaths[key.Path] = true
	}

	documented := map[string]bool{}

	var unknown []string

	for _, entry := range pageKeys(string(read(t, bootstrapPage))) {
		prefix, token, _ := strings.Cut(entry, "\x00")

		switch {
		case prefix != "" && schemaPaths[prefix+"."+token]:
			documented[prefix+"."+token] = true
		case schemaPaths[token]:
			documented[token] = true
		default:
			unknown = append(unknown, entry)
		}
	}

	var missing []string

	for _, key := range keys {
		if !documented[key.Path] && !hasDocumentedChild(key.Path, documented) {
			missing = append(missing, key.Path)
		}
	}

	assert.Empty(t, missing, "keys of the schema that the page does not list")
	assert.Empty(t, unknown, "keys of the page that the schema does not have")
}

func hasDocumentedChild(path string, documented map[string]bool) bool {
	for other := range documented {
		if strings.HasPrefix(other, path+".") || strings.HasPrefix(other, path+"[]") {
			return true
		}
	}

	return false
}
