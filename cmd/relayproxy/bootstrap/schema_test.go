package bootstrap_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// TestCommittedSchemaIsCurrent fails when .schema/bootstrap-schema.json is stale. Regenerate it with
// go run ./cmd/jsonschema-generator --schema-location .schema/flag-schema.json
// --bootstrap-schema-location .schema/bootstrap-schema.json.
func TestCommittedSchemaIsCurrent(t *testing.T) {
	t.Parallel()

	got, err := bootstrap.JSONSchema()
	require.NoError(t, err)

	want, err := os.ReadFile("../../../.schema/bootstrap-schema.json")
	require.NoError(t, err)

	assert.Equal(t, string(want), string(got), "the committed schema is stale: run the jsonschema-generator")
}

func TestSchemaShape(t *testing.T) {
	t.Parallel()

	raw, err := bootstrap.JSONSchema()
	require.NoError(t, err)

	var doc struct {
		Defs map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
			OneOf      []json.RawMessage          `json:"oneOf"`
		} `json:"$defs"`
	}

	require.NoError(t, json.Unmarshal(raw, &doc))

	tests := []struct {
		name         string
		def          string
		wantProperty string
		wantRequired []string
		wantOneOf    int
	}{
		{name: "root has the version key", def: "Config", wantProperty: "version", wantRequired: []string{"version"}},
		{name: "control plane", def: "Manifest", wantProperty: "maxObjects", wantRequired: []string{"key"}},
		{name: "connection is a union", def: "Connection", wantOneOf: 4},
		{name: "s3 member", def: "S3Connection", wantProperty: "bucket", wantRequired: []string{"type"}},
		{name: "secret reference", def: "Secret", wantProperty: "fromEnv"},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			def, ok := doc.Defs[tcase.def]
			require.True(t, ok)

			if tcase.wantProperty != "" {
				assert.Contains(t, def.Properties, tcase.wantProperty)
			}

			assert.Len(t, def.OneOf, tcase.wantOneOf)
			assert.Equal(t, tcase.wantRequired, def.Required)
		})
	}
}
