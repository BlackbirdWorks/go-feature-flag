package bootstrap_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// emptyValueCase writes an empty string for one enumerated setting. The loader treats an empty value as unset and
// applies the default, except for the discriminators, which have no default.
type emptyValueCase struct {
	setting    string
	doc        string
	wantAccept bool
}

// emptyValueCases is the one list of the enumerated settings: it is the source of the paired loader and schema
// cases, and of the completeness guard below.
func emptyValueCases() []emptyValueCase {
	return []emptyValueCase{
		{"Listener.role", "server: {listeners: [{name: api, address: \":1\", role: \"\"}]}", true},
		{"Logging.level", "observability: {logging: {level: \"\"}}", true},
		{"Logging.format", "observability: {logging: {format: \"\"}}", true},
		{"Tracing.sampler", "observability: {tracing: {sampler: \"\"}}", true},
		{"Credentials.type", "connections: {a: {type: s3, credentials: {type: \"\"}}}", true},
		{
			"S3Connection.requestChecksumCalculation",
			"connections: {a: {type: s3, requestChecksumCalculation: \"\"}}",
			true,
		},
		{
			"S3Connection.responseChecksumValidation",
			"connections: {a: {type: s3, responseChecksumValidation: \"\"}}",
			true,
		},
		{"Exporter.format", "exporters: {a: {kind: file, outputDir: /tmp, format: \"\"}}", true},
		{"S3Connection.type", "connections: {a: {type: \"\"}}", false},
		{"KinesisConnection.type", "connections: {a: {type: \"\", stream: s}}", false},
		{"SQSConnection.type", "connections: {a: {type: \"\", queueUrl: \"https://q.example/q\"}}", false},
		{"RedshiftConnection.type", "connections: {a: {type: \"\", region: us-east-1}}", false},
		{"Config.version", "version: \"\"", false},
		{"Exporter.kind", "exporters: {a: {kind: \"\"}}", false},
		{"Notifier.kind", "notifiers: {a: {kind: \"\"}}", false},
	}
}

func TestEmptyEnumValues(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../.schema/bootstrap-schema.json")
	require.NoError(t, err)

	checker, root, err := newSchemaChecker(raw)
	require.NoError(t, err)

	for _, tcase := range emptyValueCases() {
		t.Run(tcase.setting, func(t *testing.T) {
			t.Parallel()

			doc := "version: 2\n" + tcase.doc + "\n"
			if strings.HasPrefix(tcase.doc, "version:") {
				doc = tcase.doc + "\n"
			}

			var value any

			require.NoError(t, yaml.Unmarshal([]byte(doc), &value))

			_, parseErr := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))

			assert.Equal(t, tcase.wantAccept, parseErr == nil, "the loader: %v", parseErr)
			assert.Equal(t, tcase.wantAccept, len(checker.check("", root, value)) == 0, "the schema")
		})
	}
}

// TestEveryEnumSettingHasAnEmptyValueCase finds every enumerated property of the schema and checks that the list
// of cases covers it: a property whose enum accepts the empty string needs an accepted case, and any other
// needs a rejected one.
func TestEveryEnumSettingHasAnEmptyValueCase(t *testing.T) {
	t.Parallel()

	raw, err := bootstrap.JSONSchema()
	require.NoError(t, err)

	var doc struct {
		Defs map[string]struct {
			Properties map[string]struct {
				Enum []any `json:"enum"`
			} `json:"properties"`
		} `json:"$defs"`
	}

	require.NoError(t, json.Unmarshal(raw, &doc))

	acceptsEmpty, rejectsEmpty := map[string]bool{}, map[string]bool{}

	for def, body := range doc.Defs {
		for property, schema := range body.Properties {
			if len(schema.Enum) == 0 {
				continue
			}

			key := fmt.Sprintf("%s.%s", def, property)
			if slices.Contains(schema.Enum, any("")) {
				acceptsEmpty[key] = true
			} else {
				rejectsEmpty[key] = true
			}
		}
	}

	wantAccept, wantReject := map[string]bool{}, map[string]bool{}

	for _, tcase := range emptyValueCases() {
		if tcase.wantAccept {
			wantAccept[tcase.setting] = true
		} else {
			wantReject[tcase.setting] = true
		}
	}

	assert.ElementsMatch(t, slices.Sorted(maps.Keys(wantAccept)), slices.Sorted(maps.Keys(acceptsEmpty)))
	assert.ElementsMatch(t, slices.Sorted(maps.Keys(wantReject)), slices.Sorted(maps.Keys(rejectsEmpty)))
}
