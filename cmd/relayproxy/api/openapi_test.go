package api_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
)

// Test_OpenAPI_Golden compares the document generated from the registered operations with the committed copy: a
// change of the API shows in the review. GOLDEN_UPDATE=1 rewrites the file.
func Test_OpenAPI_Golden(t *testing.T) {
	t.Parallel()

	const file = "testdata/openapi.golden.json"
	got, err := api.OpenAPIJSON()
	require.NoError(t, err)

	if os.Getenv("GOLDEN_UPDATE") != "" {
		require.NoError(t, os.WriteFile(file, got, 0o600))

		return
	}
	want, err := os.ReadFile(file)
	require.NoError(t, err, "missing golden file, run with GOLDEN_UPDATE=1")
	assert.Equal(t, string(want), string(got))
}
