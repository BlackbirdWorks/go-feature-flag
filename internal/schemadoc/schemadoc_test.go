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
	perKind        = "\x00perKind"
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

// pageRow is a row of a key table of the page: the keys of its Key cell and its Type and Default cells.
type pageRow struct {
	prefix string
	typ    string
	def    string
	keys   []string
	// perKind says that the table has a column for the connection type: the schema merges the defaults of the types.
	perKind bool
}

// pageRows returns the rows of the key tables of the page, with the prefix of the section heading that applies to the
// relative keys. A key table is a table with a column named Key.
func pageRows(page string) []pageRow {
	var out []pageRow

	prefix := ""
	columns := map[string]int{}

	section, _, _ := strings.Cut(page, keysSectionEnd)
	_, section, _ = strings.Cut(section, "\n## Keys and defaults")

	for line := range strings.SplitSeq(section, "\n") {
		if strings.HasPrefix(line, "#") {
			prefix, columns = "", map[string]int{}

			if match := headingKey.FindStringSubmatch(line); match != nil {
				prefix = match[1]
			}

			continue
		}

		if !strings.HasPrefix(line, "|") {
			columns = map[string]int{}

			continue
		}

		cells := strings.Split(strings.Trim(line, "| "), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}

		if slices.Contains(cells, "Key") {
			columns = headerColumns(cells)

			continue
		}

		keyColumn, hasKey := columns["Key"]
		if !hasKey || keyColumn >= len(cells) || strings.HasPrefix(cells[0], "---") {
			continue
		}

		_, kind := columns[perKind]

		row := pageRow{
			perKind: kind,
			prefix:  prefix,
			typ:     cellAt(cells, columns, "Type"),
			def:     cellAt(cells, columns, "Default"),
		}
		for _, match := range backtickSpan.FindAllStringSubmatch(cells[keyColumn], -1) {
			row.keys = append(row.keys, match[1])
		}

		out = append(out, row)
	}

	return out
}

// headerColumns maps the names of the header cells to their index. The last column of a name wins: the Type of a key,
// not of a connection. A table that starts with a Type column is a table per connection type.
func headerColumns(cells []string) map[string]int {
	columns := map[string]int{}

	for i, cell := range cells {
		columns[cell] = i

		if cell == "Type" && i > 0 && cells[0] == "Type" {
			columns[perKind] = i
		}
	}

	return columns
}

func cellAt(cells []string, columns map[string]int, name string) string {
	index, found := columns[name]
	if !found || index >= len(cells) {
		return ""
	}

	return cells[index]
}

// typeMatches says that the type cell of the page names a type of the schema: "list" for "list of string", "map" for
// "map of secret", "string" for "string or object".
func typeMatches(page, schema string) bool {
	return slices.Contains(strings.Fields(strings.ReplaceAll(schema, " or ", " ")), strings.Fields(page + " ")[0])
}

// defaultMatches says that the default cell holds the default of the schema. A key that several connection types share
// has the merged default of the types, so the rows of a table per type are checked only for a key of one type.
func defaultMatches(row *pageRow, key *schemadoc.Key) bool {
	if key.Default == "" || (row.perKind && len(key.OnlyFor) != 1) {
		return true
	}

	return strings.Contains(strings.ReplaceAll(row.def, "`", ""), key.Default)
}

// Every key of the bootstrap schema is on the bootstrap reference page, and every key of the page is in the schema.
// An object that has documented keys below it needs no row of its own. The type of a row is a type of the schema, and
// the default of a row holds the default that the schema states.
func TestBootstrapPageListsEveryKey(t *testing.T) {
	t.Parallel()

	keys, err := schemadoc.Keys(read(t, bootstrapPath))
	require.NoError(t, err)

	schema := map[string]schemadoc.Key{}
	for _, key := range keys {
		schema[key.Path] = key
	}

	documented := map[string]bool{}

	var unknown, wrongType, wrongDefault []string

	for _, row := range pageRows(string(read(t, bootstrapPage))) {
		for _, token := range row.keys {
			path := token
			if _, found := schema[row.prefix+"."+token]; row.prefix != "" && found {
				path = row.prefix + "." + token
			}

			key, found := schema[path]
			if !found {
				unknown = append(unknown, row.prefix+" "+token)

				continue
			}

			documented[path] = true

			if len(row.keys) > 1 {
				continue // one type and default cell for several keys
			}

			if row.typ == "" || !typeMatches(row.typ, key.Type) {
				wrongType = append(wrongType, path+": page "+row.typ+", schema "+key.Type)
			}

			if !defaultMatches(&row, &key) {
				wrongDefault = append(wrongDefault, path+": page "+row.def+", schema "+key.Default)
			}
		}
	}

	assert.Empty(t, undocumented(keys, documented), "keys of the schema that the page does not list")
	assert.Empty(t, unknown, "keys of the page that the schema does not have")
	assert.Empty(t, wrongType, "rows whose type is not a type of the schema")
	assert.Empty(t, wrongDefault, "rows whose default is not the default of the schema")
}

// undocumented returns the paths of the keys that neither a row nor a row below them documents.
func undocumented(keys []schemadoc.Key, documented map[string]bool) []string {
	var missing []string

	for i := range keys {
		if path := keys[i].Path; !documented[path] && !hasDocumentedChild(path, documented) {
			missing = append(missing, path)
		}
	}

	return missing
}

func hasDocumentedChild(path string, documented map[string]bool) bool {
	for other := range documented {
		if strings.HasPrefix(other, path+".") || strings.HasPrefix(other, path+"[]") {
			return true
		}
	}

	return false
}
