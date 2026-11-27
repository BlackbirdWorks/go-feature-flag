package api_test

import (
	"os"
	"slices"
	"strings"
	"testing"

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

// The operations the anonymous flag set can serve document an optional key (the empty requirement next to the key
// schemes); the admin operations and the other ones keep a required key.
func TestOpenAPIOptionalKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		path         string
		wantOptional bool
	}{
		{
			name:         "the OFREP bulk evaluation",
			path:         "/ofrep/v1/evaluate/flags",
			wantOptional: true,
		},
		{name: "the all flags evaluation", path: "/v1/allflags", wantOptional: true},
		{name: "the validate endpoint", path: "/admin/v1/flags/validate"},
		{name: "the refresh endpoint", path: "/admin/v1/retriever/refresh"},
	}

	doc := api.OpenAPI()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			item := doc.Paths[tt.path]
			require.NotNil(t, item)

			operation := item.Post
			require.NotNil(t, operation)

			hasEmpty := slices.ContainsFunc(
				operation.Security,
				func(req map[string][]string) bool { return len(req) == 0 },
			)

			assert.Equal(t, tt.wantOptional, hasEmpty, "the empty requirement")
			assert.Equal(
				t,
				tt.wantOptional,
				strings.Contains(operation.Description, "auth.anonymousFlagSet"),
				"the note",
			)
		})
	}
}
