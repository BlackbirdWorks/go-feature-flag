package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
)

// Every shipped example loads with the validation of the relay, maps to server settings, and the ones that read local
// files build a core (the relative store path of an example is made absolute, the tests run in another directory).
func TestShippedExamplesStart(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	tests := []struct {
		name     string
		file     string
		store    string
		wantPort int
	}{
		{
			name:     "docker example",
			file:     "cmd/relayproxy/testdata/dockerhub-example/bootstrap.yaml",
			store:    "cmd/relayproxy/testdata/dockerhub-example/store",
			wantPort: 1031,
		},
		{name: "provider tests relay", file: "openfeature/provider_tests/bootstrap.yaml",
			store: "openfeature/provider_tests/store", wantPort: 1031},
		{name: "provider tests relay with keys", file: "openfeature/provider_tests/bootstrap-authenticated.yaml",
			store: "openfeature/provider_tests/store", wantPort: 1032},
		{
			name:     "complete example of the loader",
			file:     "cmd/relayproxy/bootstrap/testdata/bootstrap.yaml",
			wantPort: 1031,
		},
	}

	secrets := map[string]string{
		"DEV_S3_ACCESS_KEY": "a", "SEMAPHORE_ADMIN_KEY": "b", "EVALS_WEBHOOK_URL": "https://example.com/e",
		"EVALS_WEBHOOK_TOKEN": "c", "SLACK_WEBHOOK_URL": "https://hooks.example.com/s",
	}
	load := bootstrap.LoadOptions{
		ReadFile: os.ReadFile,
		LookupEnv: func(name string) (string, bool) {
			v, ok := secrets[name]

			return v, ok
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(root, tt.file)

			cfg, err := bootstrap.Load(&bootstrap.Overrides{Config: path}, load)
			require.NoError(t, err)

			settings, err := corehttp.SettingsFrom(cfg, "test")
			require.NoError(t, err)
			assert.Equal(t, tt.wantPort, settings.Port)

			if tt.store == "" {
				return
			}

			conn := cfg.Connections["control"]
			require.NotNil(t, conn.File)

			conn.File.Path = filepath.Join(root, tt.store)

			core, err := relaycore.New(t.Context(), cfg, &relaycore.Deps{Secrets: load})
			require.NoError(t, err)
			require.NoError(t, core.Shutdown(t.Context()))
		})
	}
}
