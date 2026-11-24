package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestValidateServerConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		wantErr string
		server  Server
	}{
		{name: "http mode needs nothing else", server: Server{Mode: ServerModeHTTP}},
		{name: "h2c is an http option", server: Server{Mode: ServerModeHTTP, EnableH2C: true}},
		{name: "unix socket without a path", server: Server{Mode: ServerModeUnixSocket},
			wantErr: "unixSocketPath must be set"},
		{
			name:   "unix socket with a path",
			server: Server{Mode: ServerModeUnixSocket, UnixSocketPath: "/tmp/semaphore.sock"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := (&Config{Server: tt.server}).validateServerConfig()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)

				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestValidateExporters(t *testing.T) {
	t.Parallel()
	valid := ExporterConf{Kind: LogExporter}
	invalid := ExporterConf{Kind: FileExporter} // no outputDir
	tests := []struct {
		name      string
		exporter  *ExporterConf
		exporters *[]ExporterConf
		wantErr   string
	}{
		{name: "none"},
		{name: "valid single exporter", exporter: &valid},
		{name: "invalid single exporter", exporter: &invalid, wantErr: "outputDir"},
		{name: "valid list", exporters: &[]ExporterConf{valid, valid}},
		{name: "the index of the invalid exporter is reported", exporters: &[]ExporterConf{valid, invalid},
			wantErr: "exporter at index 1 validation failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateExporters(tt.exporter, tt.exporters)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)

				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestAttachConfigChangeCallback(t *testing.T) {
	t.Parallel()
	tests := []struct {
		config *Config
		name   string
		wantN  int
	}{
		{name: "without a loader nothing is attached", config: &Config{logger: zap.NewNop()}},
		{name: "without a loader and a logger it does not panic", config: &Config{}},
		{name: "with a loader the callback is registered",
			config: &Config{logger: zap.NewNop(), configLoader: &Loader{}}, wantN: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.config.AttachConfigChangeCallback(func(*Config) {})
			if tt.config.configLoader != nil {
				assert.Len(t, tt.config.configLoader.callbacks, tt.wantN)
			}
			assert.NoError(t, tt.config.StopConfigChangeWatcher())
		})
	}
}
