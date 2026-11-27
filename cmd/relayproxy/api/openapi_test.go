package api_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
)

// TestOpenAPI compares the document generated from the registered operations with the committed copy: a change of
// the API shows in the review. GOLDEN_UPDATE=1 rewrites the files.
func TestOpenAPI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
	}{
		{name: "document", file: "testdata/openapi.golden.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := api.OpenAPIJSON()
			require.NoError(t, err)

			if os.Getenv(goldenUpdate) != "" {
				require.NoError(t, os.WriteFile(tt.file, got, 0o600))

				return
			}

			want, err := os.ReadFile(tt.file)
			require.NoError(t, err, "missing golden file, run with GOLDEN_UPDATE=1")
			assert.Equal(t, string(want), string(got))
		})
	}
}

// The operations the anonymous flag set can reach document an optional key (the empty requirement next to the key
// schemes) and say so; no other operation does, the admin and the monitoring ones among them.
func TestOpenAPIOptionalKey(t *testing.T) {
	t.Parallel()

	want := []string{
		"allFlags", "collectEvalData", "flagChange", "flagConfiguration", "flagEval", "getManifest",
		"ofrepBulkEvaluate", "ofrepEvaluate", "sseFlagChange", "wsFlagChange", "wsFlagChangeLegacy",
	}

	var optional, noted []string

	for _, item := range api.OpenAPI().Paths {
		for _, operation := range []*huma.Operation{item.Get, item.Post, item.Put, item.Delete} {
			if operation == nil {
				continue
			}

			if slices.ContainsFunc(operation.Security, func(req map[string][]string) bool { return len(req) == 0 }) {
				optional = append(optional, operation.OperationID)
			}

			if strings.Contains(operation.Description, "auth.anonymousFlagSet") {
				noted = append(noted, operation.OperationID)
			}
		}
	}

	slices.Sort(optional)
	slices.Sort(noted)

	assert.Equal(t, want, optional, "the operations with an optional key")
	assert.Equal(t, want, noted, "the operations that say why")
}
