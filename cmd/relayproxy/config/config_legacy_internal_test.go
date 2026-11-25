package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/knadh/koanf/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/legacy"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/testhelpers/slogutil"
)

const (
	goffProxyYamlPath      = "0/goff-proxy.yaml"
	semaphoreProxyYamlPath = "0/semaphore-proxy.yaml"
	semaphoreServerPortTwo = "SEMAPHORE_SERVER_PORT=2"
	serverPortOne          = "SERVER_PORT=1"
)

// recordingWarner returns a warner and the kinds it was asked to warn about.
func recordingWarner() (*legacy.Warner, func() []string) {
	var (
		mu    sync.Mutex
		kinds []string
	)

	w := legacy.NewWarner(func(kind, _ string) {
		mu.Lock()
		defer mu.Unlock()

		kinds = append(kinds, kind)
	})

	return w, func() []string {
		mu.Lock()
		defer mu.Unlock()

		return append([]string(nil), kinds...)
	}
}

func TestLocateConfigFileLegacyNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// files to create, as <folder index>/<file name>
		files        []string
		wantFile     string
		wantWarnings []string
		wantErr      bool
	}{
		{name: "semaphore-proxy.yaml", files: []string{semaphoreProxyYamlPath}, wantFile: semaphoreProxyYamlPath},
		{
			name:         "legacy goff-proxy.yaml still found",
			files:        []string{goffProxyYamlPath},
			wantFile:     goffProxyYamlPath,
			wantWarnings: []string{legacy.KindConfigFile},
		},
		{name: "new name wins over the legacy one in the same folder",
			files:    []string{goffProxyYamlPath, semaphoreProxyYamlPath},
			wantFile: semaphoreProxyYamlPath},
		{name: "new name wins whatever the extension",
			files:    []string{goffProxyYamlPath, "0/semaphore-proxy.json"},
			wantFile: "0/semaphore-proxy.json"},
		{name: "every extension is supported for both names",
			files: []string{"0/goff-proxy.toml"}, wantFile: "0/goff-proxy.toml",
			wantWarnings: []string{legacy.KindConfigFile}},
		{name: "first folder wins", files: []string{"0/goff-proxy.yml", "1/semaphore-proxy.yml"},
			wantFile: "0/goff-proxy.yml", wantWarnings: []string{legacy.KindConfigFile}},
		{name: "second folder", files: []string{"1/semaphore-proxy.yaml"}, wantFile: "1/semaphore-proxy.yaml"},
		{name: "no file", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()

			locations := []string{filepath.Join(root, "0") + "/", filepath.Join(root, "1") + "/"}
			for _, f := range tt.files {
				path := filepath.Join(root, f)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
				require.NoError(t, os.WriteFile(path, []byte("x: 1"), 0o600))
			}

			warner, warnings := recordingWarner()

			got, err := locateConfigFileIn("", locations, warner)
			if tt.wantErr {
				require.Error(t, err)
				assert.Empty(t, warnings())

				return
			}

			require.NoError(t, err)
			assert.Equal(t, filepath.Join(root, tt.wantFile), got)
			assert.Equal(t, tt.wantWarnings, warnings())

			// asking again must not warn a second time
			_, err = locateConfigFileIn("", locations, warner)
			require.NoError(t, err)
			assert.Equal(t, tt.wantWarnings, warnings())
		})
	}
}

func TestLocateConfigFileExplicitPath(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "goff-proxy.yaml")
	require.NoError(t, os.WriteFile(path, []byte("x: 1"), 0o600))

	warner, warnings := recordingWarner()

	got, err := locateConfigFileIn(path, nil, warner)
	require.NoError(t, err)
	assert.Equal(t, path, got)
	assert.Empty(t, warnings(), "a path given with --config is the choice of the user, whatever its name")

	_, err = locateConfigFileIn(filepath.Join(t.TempDir(), "missing.yaml"), nil, warner)
	assert.Error(t, err)
}

func TestLoadEnvVariablesPrefixes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		configPrefix string
		environ      []string
		wantPort     string
		wantLogLevel string
		wantWarnings []string
	}{
		{name: "unprefixed variables keep working",
			environ:  []string{serverPortOne, "LOGLEVEL=debug"},
			wantPort: "1", wantLogLevel: fixtures.LogLevelDebug},
		{name: "SEMAPHORE_ prefix is always read",
			environ:  []string{semaphoreServerPortTwo, "SEMAPHORE_LOGLEVEL=warn"},
			wantPort: "2", wantLogLevel: "warn"},
		{name: "SEMAPHORE_ wins over the unprefixed variable",
			environ:  []string{serverPortOne, semaphoreServerPortTwo},
			wantPort: "2"},
		{name: "configured prefix keeps working",
			configPrefix: "MYAPP_", environ: []string{"MYAPP_SERVER_PORT=3", serverPortOne},
			wantPort: "3"},
		{name: "SEMAPHORE_ wins over the configured prefix",
			configPrefix: "MYAPP_", environ: []string{"MYAPP_SERVER_PORT=3", semaphoreServerPortTwo},
			wantPort: "2"},
		{name: "configured SEMAPHORE_ prefix is not loaded twice",
			configPrefix: "SEMAPHORE_", environ: []string{"SEMAPHORE_SERVER_PORT=4"},
			wantPort: "4"},
		{name: "legacy GOFF_ prefix keeps working and warns once",
			configPrefix: "GOFF_", environ: []string{"GOFF_SERVER_PORT=5"},
			wantPort: "5", wantWarnings: []string{legacy.KindEnvPrefix}},
		{name: "legacy GOFFPROXY_ prefix keeps working and warns once",
			configPrefix: "GOFFPROXY_", environ: []string{"GOFFPROXY_SERVER_PORT=6"},
			wantPort: "6", wantWarnings: []string{legacy.KindEnvPrefix}},
		{name: "no variable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			warner, warnings := recordingWarner()

			loader := &Loader{
				log:         slog.New(slog.DiscardHandler),
				k:           koanf.New("."),
				environFunc: func() []string { return tt.environ },
				warner:      warner,
			}
			if tt.configPrefix != "" {
				require.NoError(t, loader.k.Set("envVariablePrefix", tt.configPrefix))
			}

			loader.loadEnvVariables()
			loader.loadEnvVariables() // a reload must not warn again

			assert.Equal(t, tt.wantPort, loader.k.String("server.port"))
			assert.Equal(t, tt.wantLogLevel, loader.k.String("loglevel"))
			assert.Equal(t, tt.wantWarnings, warnings())
		})
	}
}

func TestLoadEnvVariablesSemaphoreArrays(t *testing.T) {
	t.Parallel()

	loader := &Loader{
		log: slog.New(slog.DiscardHandler),
		k:   koanf.New("."),
		environFunc: func() []string {
			return []string{
				"SEMAPHORE_RETRIEVERS_0_KIND=file",
				"SEMAPHORE_RETRIEVERS_0_PATH=/tmp/flags.yaml",
				"SEMAPHORE_AUTHORIZEDKEYS_EVALUATION=a,b",
			}
		},
	}
	loader.loadEnvVariables()

	conf, err := loader.ToConfig()
	require.NoError(t, err)
	require.NotNil(t, conf.Retrievers)
	require.Len(t, *conf.Retrievers, 1)
	assert.Equal(t, "/tmp/flags.yaml", (*conf.Retrievers)[0].Path)
	assert.Equal(t, []string{"a", "b"}, loader.k.Strings("authorizedkeys.evaluation"))
}

func TestEnvironProviderReadBytesIsNotSupported(t *testing.T) {
	t.Parallel()

	got, err := (&environProvider{}).ReadBytes()
	assert.Nil(t, got)
	assert.EqualError(t, err, "env provider does not support this method")
}

func TestLegacyCompatibilityOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		environ            []string
		wantLegacyMetrics  bool
		wantNoSemaphoreKey bool
	}{
		{name: "off by default"},
		{name: "legacy metrics", environ: []string{"SEMAPHORE_EXPOSELEGACYMETRICS=true"}, wantLegacyMetrics: true},
		{name: "legacy metadata only", environ: []string{"DISABLESEMAPHOREMETADATA=true"}, wantNoSemaphoreKey: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			loader := &Loader{
				log:         slog.New(slog.DiscardHandler),
				k:           koanf.New("."),
				environFunc: func() []string { return tt.environ },
			}
			loader.loadEnvVariables()
			conf, err := loader.ToConfig()
			require.NoError(t, err)
			assert.Equal(t, tt.wantLegacyMetrics, conf.ExposeLegacyMetrics)
			assert.Equal(t, tt.wantNoSemaphoreKey, conf.DisableSemaphoreMetadata)
		})
	}
}

func TestLoadEnvVariablesEnableH2C(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		environ []string
		want    bool
	}{
		{name: "off by default"},
		{name: "unprefixed variable", environ: []string{"SERVER_ENABLEH2C=true"}, want: true},
		{name: "SEMAPHORE_ variable", environ: []string{"SEMAPHORE_SERVER_ENABLEH2C=true"}, want: true},
		{name: "SEMAPHORE_ wins", environ: []string{"SERVER_ENABLEH2C=true", "SEMAPHORE_SERVER_ENABLEH2C=false"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			loader := &Loader{
				log:         slog.New(slog.DiscardHandler),
				k:           koanf.New("."),
				environFunc: func() []string { return tt.environ },
			}
			loader.loadEnvVariables()
			conf, err := loader.ToConfig()
			require.NoError(t, err)
			assert.Equal(t, tt.want, conf.Server.EnableH2C)
		})
	}
}

func TestLoadEnvVariablesKubernetesServiceLinks(t *testing.T) {
	t.Parallel()
	// what Kubernetes injects for a Service named "semaphore" exposing port 1031
	serviceLinks := []string{
		"SEMAPHORE_SERVICE_HOST=10.0.0.1",
		"SEMAPHORE_SERVICE_PORT=1031",
		"SEMAPHORE_SERVICE_PORT_HTTP=1031",
		"SEMAPHORE_PORT=tcp://10.0.0.1:1031",
		"SEMAPHORE_PORT_1031_TCP=tcp://10.0.0.1:1031",
		"SEMAPHORE_PORT_1031_TCP_PROTO=tcp",
		"SEMAPHORE_PORT_1031_TCP_PORT=1031",
		"SEMAPHORE_PORT_1031_TCP_ADDR=10.0.0.1",
		"SEMAPHORE_PORT_1031_UDP=udp://10.0.0.1:1031",
	}

	tests := []struct {
		name        string
		environ     []string
		wantPort    string
		wantHost    string
		wantIgnored []string
	}{
		{name: "service links alone are ignored", environ: serviceLinks, wantIgnored: []string{
			"SEMAPHORE_PORT", "SEMAPHORE_PORT_1031_TCP", "SEMAPHORE_PORT_1031_TCP_ADDR",
			"SEMAPHORE_PORT_1031_TCP_PORT", "SEMAPHORE_PORT_1031_TCP_PROTO", "SEMAPHORE_PORT_1031_UDP",
			"SEMAPHORE_SERVICE_HOST", "SEMAPHORE_SERVICE_PORT", "SEMAPHORE_SERVICE_PORT_HTTP"}},
		{name: "real settings next to service links are kept",
			environ:  append([]string{"SEMAPHORE_SERVER_PORT=1031", "SEMAPHORE_HOST=example.org"}, serviceLinks...),
			wantPort: "1031", wantHost: "example.org", wantIgnored: []string{
				"SEMAPHORE_PORT", "SEMAPHORE_PORT_1031_TCP", "SEMAPHORE_PORT_1031_TCP_ADDR",
				"SEMAPHORE_PORT_1031_TCP_PORT", "SEMAPHORE_PORT_1031_TCP_PROTO", "SEMAPHORE_PORT_1031_UDP",
				"SEMAPHORE_SERVICE_HOST", "SEMAPHORE_SERVICE_PORT", "SEMAPHORE_SERVICE_PORT_HTTP"}},
		{name: "a port without the service link form is a setting", environ: []string{"SEMAPHORE_PORT=1031"}},
		{name: "a service host that is not an address is a setting",
			environ: []string{"SEMAPHORE_SERVICE_HOST=not an ip"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			core, logs := slogutil.NewObserver(slog.LevelDebug)
			loader := &Loader{
				log:         core,
				k:           koanf.New("."),
				environFunc: func() []string { return tt.environ },
			}
			loader.loadEnvVariables()
			conf, err := loader.ToConfig()
			require.NoError(t, err)
			assert.Equal(t, tt.wantHost, conf.Host)

			if tt.wantPort != "" {
				assert.Equal(t, 1031, conf.Server.Port)
			}

			var ignored []string

			for _, entry := range logs.FilterMessage("config: ignoring Kubernetes service link variables").All() {
				names, ok := entry.ContextMap()["names"].([]string)
				require.True(t, ok)

				ignored = append(ignored, names...)

				for k, v := range entry.ContextMap() {
					assert.NotContains(t, fmtAny(v), "10.0.0.1", "values must not be logged (%s)", k)
				}
			}

			assert.Equal(t, tt.wantIgnored, ignored)
		})
	}
}

func fmtAny(v any) string { return fmt.Sprint(v) }
