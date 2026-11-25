package api_test

import (
	"os"
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

			if os.Getenv("GOLDEN_UPDATE") != "" {
				require.NoError(t, os.WriteFile(tt.file, got, 0o600))

				return
			}

			want, err := os.ReadFile(tt.file)
			require.NoError(t, err, "missing golden file, run with GOLDEN_UPDATE=1")
			assert.Equal(t, string(want), string(got))
		})
	}
}
