package config_test

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/config"
	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/controlplane"
)

// TestShippedExamplesValidate runs `config validate` on every bootstrap file the repository ships as an example
// or as a fixture of the provider tests.
func TestShippedExamplesValidate(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	tests := []struct {
		name string
		file string
	}{
		{name: "docker example", file: "cmd/relayproxy/testdata/dockerhub-example/bootstrap.yaml"},
		{name: "provider tests relay", file: "openfeature/provider_tests/bootstrap.yaml"},
		{name: "provider tests relay with keys", file: "openfeature/provider_tests/bootstrap-authenticated.yaml"},
		{name: "complete example of the loader", file: "cmd/relayproxy/bootstrap/testdata/bootstrap.yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(root, tt.file)

			deps := config.DefaultDeps()
			// the complete example names secrets by reference: the validation resolves them
			deps.LookupEnv = func(name string) (string, bool) {
				secrets := map[string]string{
					"DEV_S3_ACCESS_KEY": "a", "SEMAPHORE_ADMIN_KEY": "b", "EVALS_WEBHOOK_URL": "https://example.com/e",
					"EVALS_WEBHOOK_TOKEN": "c", "SLACK_WEBHOOK_URL": "https://hooks.example.com/s",
				}
				v, ok := secrets[name]

				return v, ok
			}

			var out, errOut bytes.Buffer

			err := config.Run([]string{cmdValidate, path}, &helper.Streams{Stdout: &out, Stderr: &errOut}, deps)
			require.NoError(t, err, out.String())
			assert.Empty(t, errOut.String())
			assert.Equal(t, path+": valid\n", out.String())
		})
	}
}

// TestShippedStoresAreConsistent checks that the manifest of every shipped file store is valid and that every object it
// lists is there with the size and the checksum it says.
func TestShippedStoresAreConsistent(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	tests := []struct {
		name string
		dir  string
	}{
		{name: "docker example", dir: "cmd/relayproxy/testdata/dockerhub-example/store"},
		{name: "provider tests", dir: "openfeature/provider_tests/store"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(root, tt.dir)

			raw, err := os.ReadFile(filepath.Join(dir, "manifests", "stable.yaml"))
			require.NoError(t, err)

			manifest, _, err := controlplane.UnmarshalManifest(raw)
			require.NoError(t, err)
			require.NoError(t, controlplane.ValidateManifest(manifest))
			assert.Len(t, manifest.Objects, 2)

			for i := range manifest.Objects {
				entry := &manifest.Objects[i]

				data, readErr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(entry.Key)))
				require.NoError(t, readErr)
				assert.Equal(t, entry.Checksum, controlplane.ChecksumOf(data), entry.Name)
				assert.Equal(t, entry.Size, int64(len(data)), entry.Name)
			}
		})
	}
}

// The full example of the documentation is the complete example of the loader, and it validates: the page can not
// drift from the file the tests use.
func TestDocumentedFullExampleValidates(t *testing.T) {
	t.Parallel()

	page, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "relay-proxy", "bootstrap-config.md"))
	require.NoError(t, err)

	file, err := os.ReadFile(
		filepath.Join("..", "..", "..", "cmd", "relayproxy", "bootstrap", "testdata", "bootstrap.yaml"),
	)
	require.NoError(t, err)

	blocks := regexp.MustCompile("(?s)<!-- full-example -->\n```yaml\n(.*?)```").FindAllStringSubmatch(string(page), -1)
	require.Len(t, blocks, 1, "the page marks its full example")
	assert.Equal(t, string(file), blocks[0][1], "the page shows the complete example of the loader")

	deps := config.DefaultDeps()
	deps.ReadFile = func(string) ([]byte, error) { return []byte(blocks[0][1]), nil }
	deps.LookupEnv = shippedSecrets

	var out, errOut bytes.Buffer

	err = config.Run([]string{cmdValidate, "docs.yaml"}, &helper.Streams{Stdout: &out, Stderr: &errOut}, deps)
	require.NoError(t, err, out.String())
	assert.Empty(t, errOut.String())
	assert.Equal(t, "docs.yaml: valid\n", out.String())
}

func shippedSecrets(name string) (string, bool) {
	secrets := map[string]string{
		"DEV_S3_ACCESS_KEY": "a", "SEMAPHORE_ADMIN_KEY": "b", "EVALS_WEBHOOK_URL": "https://example.com/e",
		"EVALS_WEBHOOK_TOKEN": "c", "SLACK_WEBHOOK_URL": "https://hooks.example.com/s",
	}
	v, ok := secrets[name]

	return v, ok
}
