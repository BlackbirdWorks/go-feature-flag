package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/exporter/kafkaexporter"
)

func TestParseConfigDisableFlagDetailsInStream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		content     string
		environment string
	}{
		{name: "disabled by config file", content: "disableFlagDetailsInStream: true\n"},
		{name: "disabled by environment variable", environment: "true"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			configPath := filepath.Join(t.TempDir(), "goff-proxy.yaml")
			require.NoError(t, os.WriteFile(configPath, []byte(tt.content), 0o600))
			var environ []string
			if tt.environment != "" {
				environ = append(environ, "DISABLEFLAGDETAILSINSTREAM="+tt.environment)
			}

			flagSet := pflag.NewFlagSet("config", pflag.ContinueOnError)
			flagSet.String("config", "", "Location of your config file")
			require.NoError(t, flagSet.Parse([]string{"--config=" + configPath}))

			got, err := config.New(flagSet, zap.NewNop(), "1.X.X", withEnviron(environ...))
			require.NoError(t, err)
			assert.True(t, got.DisableFlagDetailsInStream)
		})
	}
}

func TestParseConfig_fileFromPflag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		want         *config.Config
		fileLocation string
		wantErr      assert.ErrorAssertionFunc
	}{
		{
			name:         "Valid yaml file",
			fileLocation: "../testdata/config/valid-file.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      "yaml",
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "http",
					URL:  "https://raw.githubusercontent.com/BlackbirdWorks/semaphore/main/cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
				},
				Exporter: &config.ExporterConf{
					Kind: "log",
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Swagger: config.Swagger{Enabled: true, Host: "gofeatureflag.org"},
				Version: "1.X.X",
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"apikey3",
					},
					Evaluation: []string{
						"apikey1",
						"apikey2",
					},
				},
				LogLevel: "info",
			},
			wantErr: assert.NoError,
		},
		{
			name:         "Valid yaml file with legacy key notifier (without s)",
			fileLocation: "../testdata/config/valid-file-notifier.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      "yaml",
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "http",
					URL:  "https://raw.githubusercontent.com/BlackbirdWorks/semaphore/main/cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
				},
				Exporter: &config.ExporterConf{
					Kind: "log",
				},
				StartWithRetrieverError: false,
				FixNotifiers: []config.NotifierConf{
					{Kind: config.DiscordNotifier, WebhookURL: "https://discord.com/api/webhooks/yyyy/xxxxxxx"},
				},
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version:       "1.X.X",
				EnableSwagger: true,
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"apikey3",
					},
					Evaluation: []string{
						"apikey1",
						"apikey2",
					},
				},

				LogLevel: "info",
			},
			wantErr: assert.NoError,
		},
		{
			name:         "Valid yaml file with legacy key notifier (without s)",
			fileLocation: "../testdata/config/valid-file-notifiers.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      "yaml",
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "http",
					URL:  "https://raw.githubusercontent.com/BlackbirdWorks/semaphore/main/cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
				},
				Exporter: &config.ExporterConf{
					Kind: "log",
				},
				StartWithRetrieverError: false,
				Notifiers: []config.NotifierConf{
					{Kind: config.DiscordNotifier, WebhookURL: "https://discord.com/api/webhooks/yyyy/xxxxxxx"},
				},
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version:       "1.X.X",
				EnableSwagger: true,
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"apikey3",
					},
					Evaluation: []string{
						"apikey1",
						"apikey2",
					},
				},

				LogLevel: "info",
			},
			wantErr: assert.NoError,
		},
		{
			name:         "Valid yaml file with multiple exporters",
			fileLocation: "../testdata/config/valid-yaml-multiple-exporters.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      "yaml",
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "http",
					URL:  "https://raw.githubusercontent.com/BlackbirdWorks/semaphore/main/cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
				},
				Exporters: &[]config.ExporterConf{
					{
						Kind: "log",
					},
					{
						Kind:      "file",
						OutputDir: "./",
					},
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version:       "1.X.X",
				EnableSwagger: true,
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"apikey3",
					},
					Evaluation: []string{
						"apikey1",
						"apikey2",
					},
				},
				LogLevel: "info",
			},
			wantErr: assert.NoError,
		},
		{
			name:         "Valid yaml file with both exporter and exporters",
			fileLocation: "../testdata/config/valid-yaml-exporter-and-exporters.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      "yaml",
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "http",
					URL:  "https://raw.githubusercontent.com/BlackbirdWorks/semaphore/main/cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
				},
				Exporter: &config.ExporterConf{
					Kind: "log",
				},
				Exporters: &[]config.ExporterConf{
					{
						Kind:        "webhook",
						EndpointURL: "https://example.com/webhook",
					},
					{
						Kind:      "file",
						OutputDir: "./",
					},
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version:       "1.X.X",
				EnableSwagger: true,
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"apikey3",
					},
					Evaluation: []string{
						"apikey1",
						"apikey2",
					},
				},
				LogLevel: "info",
			},
			wantErr: assert.NoError,
		},
		{
			name:         "Valid json file",
			fileLocation: "../testdata/config/valid-file.json",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      "yaml",
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "http",
					URL:  "https://raw.githubusercontent.com/BlackbirdWorks/semaphore/main/cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
				},
				Exporter: &config.ExporterConf{
					Kind: "log",
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version:       "1.X.X",
				EnableSwagger: true,
				APIKeys: []string{
					"apikey1",
					"apikey2",
				},
				LogLevel: "",
			},
			wantErr: assert.NoError,
		},
		{
			name:         "Valid toml file",
			fileLocation: "../testdata/config/valid-file.toml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      "yaml",
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "http",
					URL:  "https://raw.githubusercontent.com/BlackbirdWorks/semaphore/main/cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
				},
				Exporter: &config.ExporterConf{
					Kind: "log",
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version:       "1.X.X",
				EnableSwagger: true,
				APIKeys: []string{
					"apikey1",
					"apikey2",
				},
				LogLevel: config.DefaultLogLevel,
			},
			wantErr: assert.NoError,
		},
		{
			name:         "All default",
			fileLocation: "../testdata/config/all-default.yaml",
			want: &config.Config{
				PollingInterval:         60000,
				FileFormat:              "yaml",
				StartWithRetrieverError: false,
				Version:                 "1.X.X",
				LogLevel:                config.DefaultLogLevel,
			},
			wantErr: assert.NoError,
		},
		{
			name:         "Invalid yaml",
			fileLocation: "../testdata/config/invalid-yaml.yaml",
			wantErr:      assert.Error,
		},
		{
			name:         "Valid YAML with OTel config",
			fileLocation: "../testdata/config/valid-otel.yaml",
			want: &config.Config{
				PollingInterval: 60000,
				FileFormat:      "yaml",
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: "file",
						Path: "cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
					},
				},
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				LogLevel: config.DefaultLogLevel,
				Version:  "1.X.X",
				OtelConfig: config.OpenTelemetryConfiguration{
					Exporter: config.OtelExporter{
						Otlp: config.OtelExporterOtlp{
							Endpoint: "http://example.com:4317",
						},
					},
					Resource: config.OtelResource{
						Attributes: map[string]string{
							"foo.bar": "baz",
							"foo.baz": "bar",
						},
					},
				},
			},
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := pflag.NewFlagSet("config", pflag.ContinueOnError)
			f.String("config", "", "Location of your config file")
			_ = f.Parse([]string{fmt.Sprintf("--config=%s", tt.fileLocation)})

			got, err := config.New(f, zap.L(), "1.X.X", withEnviron("version=1.X.X"))
			if !tt.wantErr(t, err) {
				return
			}
			assert.True(t, cmp.Equal(tt.want, got, cmpopts.IgnoreUnexported(config.Config{})))
		})
	}
}

func TestParseConfig_fileFromFolder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		want         *config.Config
		fileLocation string
		wantErr      assert.ErrorAssertionFunc
	}{
		{
			name:         "Valid file",
			fileLocation: "../testdata/config/valid-file.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      "yaml",
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "http",
					URL:  "https://raw.githubusercontent.com/BlackbirdWorks/semaphore/main/cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
				},
				Exporter: &config.ExporterConf{
					Kind: "log",
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version: "1.X.X",
				Swagger: config.Swagger{Enabled: true, Host: "gofeatureflag.org"},
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"apikey3",
					},
					Evaluation: []string{
						"apikey1",
						"apikey2",
					},
				},
				LogLevel: "info",
			},
			wantErr: assert.NoError,
		},
		{
			name:         "All default",
			fileLocation: "../testdata/config/all-default.yaml",
			want: &config.Config{
				PollingInterval:         60000,
				FileFormat:              "yaml",
				StartWithRetrieverError: false,
				Version:                 "1.X.X",
				LogLevel:                config.DefaultLogLevel,
			},
			wantErr: assert.NoError,
		},
		{
			name:         "Invalid yaml",
			fileLocation: "../testdata/config/invalid-yaml.yaml",
			wantErr:      assert.Error,
		},
		{
			name:         "Should return all default if file does not exist",
			fileLocation: "../testdata/config/file-not-exist.yaml",
			wantErr:      assert.NoError,
			want: &config.Config{
				PollingInterval:         60000,
				FileFormat:              "yaml",
				StartWithRetrieverError: false,
				Version:                 "1.X.X",
				LogLevel:                config.DefaultLogLevel,
			},
		},
		{
			name:         "Should return all default if no file in the command line",
			fileLocation: "",
			wantErr:      assert.NoError,
			want: &config.Config{
				PollingInterval:         60000,
				FileFormat:              "yaml",
				StartWithRetrieverError: false,
				Version:                 "1.X.X",
				LogLevel:                config.DefaultLogLevel,
			},
		},
		{
			name:         "Should return all default if no file and no default",
			fileLocation: "",
			wantErr:      assert.NoError,
			want: &config.Config{
				PollingInterval:         60000,
				FileFormat:              "yaml",
				StartWithRetrieverError: false,
				Version:                 "1.X.X",
				LogLevel:                config.DefaultLogLevel,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := pflag.NewFlagSet("config", pflag.ContinueOnError)
			f.String("config", "", "Location of your config file")
			_ = f.Parse([]string{fmt.Sprintf("--config=%s", tt.fileLocation)})
			got, err := config.New(f, zap.L(), "1.X.X")
			if !tt.wantErr(t, err) {
				return
			}
			assert.True(t, cmp.Equal(tt.want, got, cmpopts.IgnoreUnexported(config.Config{})))
		})
	}
}

func TestConfig_IsValid(t *testing.T) {
	t.Parallel()

	type fields struct {
		Server                  config.Server
		HideBanner              bool
		Swagger                 config.Swagger
		PollingInterval         int
		FileFormat              string
		StartWithRetrieverError bool
		Retriever               *retrieverconf.RetrieverConf
		Retrievers              *[]retrieverconf.RetrieverConf
		Exporter                *config.ExporterConf
		Notifiers               []config.NotifierConf
		LogLevel                string
		Debug                   bool
		LogFormat               string
		FlagSets                []config.FlagSet
		OfrepEventStream        config.OfrepEventStream
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "invalid ofrepEventStream",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "file",
					Path: "../testdata/config/valid-file.yaml",
				},
				OfrepEventStream: config.OfrepEventStream{BaseURL: "gofeatureflag.example.com"},
			},
			wantErr: assert.Error,
		},
		{
			name:    "empty config",
			fields:  fields{},
			wantErr: assert.Error,
		},
		{
			name:    "invalid port",
			fields:  fields{},
			wantErr: assert.Error,
		},
		{
			name: "no retriever",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Notifiers: []config.NotifierConf{
					{
						Kind:        "webhook",
						EndpointURL: "https://hooktest.com/",
						Secret:      "xxxx",
					},
					{
						Kind:       "slack",
						WebhookURL: "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX",
					},
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "valid configuration",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "file",
					Path: "../testdata/config/valid-file.yaml",
				},
				Exporter: &config.ExporterConf{
					Kind:        "webhook",
					EndpointURL: "http://testingwebhook.com/test/",
					Secret:      "secret-for-signing",
					Meta: map[string]string{
						"extraInfo": "info",
					},
				},
				Notifiers: []config.NotifierConf{
					{
						Kind:        "webhook",
						EndpointURL: "https://hooktest.com/",
						Secret:      "xxxx",
					},
					{
						Kind:       "slack",
						WebhookURL: "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX",
					},
				},
				LogLevel: "info",
			},
			wantErr: assert.NoError,
		},
		{
			name: "valid configuration with notifier included",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "file",
					Path: "../testdata/config/valid-file-notifier.yaml",
				},
				Exporter: &config.ExporterConf{
					Kind:        "webhook",
					EndpointURL: "http://testingwebhook.com/test/",
					Secret:      "secret-for-signing",
					Meta: map[string]string{
						"extraInfo": "info",
					},
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "invalid retriever",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "file",
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "1 invalid retriever in the list of retrievers",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: "file",
						Path: "../testdata/config/valid-file.yaml",
					},
					{
						Kind: "file",
					},
					{
						Kind: "file",
						Path: "../testdata/config/valid-file.yaml",
					},
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "invalid exporter",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "file",
					Path: "../testdata/config/valid-file.yaml",
				},
				Exporter: &config.ExporterConf{
					Kind: "webhook",
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "invalid notifier",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "file",
					Path: "../testdata/config/valid-file.yaml",
				},
				Notifiers: []config.NotifierConf{
					{
						Kind: "webhook",
					},
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "invalid log level",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "file",
					Path: "../testdata/config/valid-file.yaml",
				},
				LogLevel: "invalid",
			},
			wantErr: assert.Error,
		},
		{
			name: "log level is not set but debug is set",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "file",
					Path: "../testdata/config/valid-file.yaml",
				},
				LogLevel: "",
				Debug:    true,
			},
			wantErr: assert.NoError,
		},
		{
			name: "invalid logFormat",
			fields: fields{
				LogFormat: "unknown",
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "file",
					Path: "../testdata/config/valid-file.yaml",
				},
				LogLevel: "info",
			},
			wantErr: assert.Error,
		},
		{
			name: "valid flagset configuration",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				FlagSets: []config.FlagSet{
					{
						Name:    "test-flagset",
						APIKeys: []string{"test-api-key"},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: "file",
							Path: "../testdata/config/valid-file.yaml",
						},
					},
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "flagset without name",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				FlagSets: []config.FlagSet{
					{
						APIKeys: []string{"test-api-key"},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: "file",
							Path: "../testdata/config/valid-file.yaml",
						},
					},
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "flagset without API keys",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				FlagSets: []config.FlagSet{
					{
						Name: "test-flagset",
						Retriever: &retrieverconf.RetrieverConf{
							Kind: "file",
							Path: "../testdata/config/valid-file.yaml",
						},
					},
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "duplicate API keys across flagsets",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				FlagSets: []config.FlagSet{
					{
						Name:    "flagset-1",
						APIKeys: []string{"shared-api-key"},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: "file",
							Path: "../testdata/config/valid-file.yaml",
						},
					},
					{
						Name:    "flagset-2",
						APIKeys: []string{"shared-api-key"},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: "file",
							Path: "../testdata/config/valid-file.yaml",
						},
					},
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "flagset with invalid retriever",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				FlagSets: []config.FlagSet{
					{
						Name:    "test-flagset",
						APIKeys: []string{"test-api-key"},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: "file",
							// Missing Path
						},
					},
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "flagset with invalid exporter",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				FlagSets: []config.FlagSet{
					{
						Name:    "test-flagset",
						APIKeys: []string{"test-api-key"},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: "file",
							Path: "../testdata/config/valid-file.yaml",
						},
						Exporter: &config.ExporterConf{
							Kind: "webhook",
							// Missing EndpointURL
						},
					},
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "flagset with invalid notifier",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				FlagSets: []config.FlagSet{
					{
						Name:    "test-flagset",
						APIKeys: []string{"test-api-key"},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: "file",
							Path: "../testdata/config/valid-file.yaml",
						},
						Notifiers: []config.NotifierConf{
							{
								Kind: "webhook",
								// Missing EndpointURL
							},
						},
					},
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "multiple valid flagsets",
			fields: fields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				FlagSets: []config.FlagSet{
					{
						Name:    "flagset-1",
						APIKeys: []string{"api-key-1"},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: "file",
							Path: "../testdata/config/valid-file.yaml",
						},
					},
					{
						Name:    "flagset-2",
						APIKeys: []string{"api-key-2"},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: "file",
							Path: "../testdata/config/valid-file.yaml",
						},
					},
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "missing unix socket path",
			fields: fields{
				LogFormat: "unknown",
				Server: config.Server{
					Mode:           config.ServerModeUnixSocket,
					UnixSocketPath: "",
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "file",
					Path: "../testdata/config/valid-file.yaml",
				},
				LogLevel: "info",
			},
			wantErr: assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &config.Config{
				PollingInterval:         tt.fields.PollingInterval,
				FileFormat:              tt.fields.FileFormat,
				StartWithRetrieverError: tt.fields.StartWithRetrieverError,
				Retriever:               tt.fields.Retriever,
				Exporter:                tt.fields.Exporter,
				Notifiers:               tt.fields.Notifiers,
				Retrievers:              tt.fields.Retrievers,
				Server:                  tt.fields.Server,
				HideBanner:              tt.fields.HideBanner,
				Swagger:                 tt.fields.Swagger,
				LogLevel:                tt.fields.LogLevel,
				LogFormat:               tt.fields.LogFormat,
				FlagSets:                tt.fields.FlagSets,
				OfrepEventStream:        tt.fields.OfrepEventStream,
			}
			if tt.name == "empty config" {
				c = nil
			}
			tt.wantErr(t, c.IsValid(), "invalid configuration")
		})
	}
}

func TestConfig_APIKeyExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config config.Config
		apiKey string
		want   bool
	}{
		{
			name: "no key in the config",
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Evaluation: []string{},
				},
			},
			apiKey: "49b67ab9-20fc-42ac-ac53-b36e29834c7",
			want:   false,
		},
		{
			name:   "key exists in a list of keys (legacy)",
			apiKey: "49b67ab9-20fc-42ac-ac53-b36e29834c7",
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Evaluation: []string{
						"0359cdb3-5fb5-4d65-b25f-b8909ec3c44",
						"fb124cf9-e058-4f34-8385-ad225ff85a3",
						"d05087dd-efff-4144-b9a6-89476a14695",
						"5082a8df-cc67-48b4-aca4-26ce1425645",
						"04d9f1b7-f50c-4407-83bb-e9c4ddc5d45",
						"62507779-bd2d-4170-b715-8d93ee7110f",
						"e0dcb798-4f97-4646-a1a9-57a6c69c235",
						"6bfd6b61-f8a9-45b3-9ca8-37125438be4",
						"aecd6aea-1350-46af-a7b9-231e9a609fd",
						"49b67ab9-20fc-42ac-ac53-b36e29834c7",
					},
				},
			},
			want: true,
		},
		{
			name:   "key exists in a list of keys",
			apiKey: "49b67ab9-20fc-42ac-ac53-b36e29834c7",
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Evaluation: []string{
						"0359cdb3-5fb5-4d65-b25f-b8909ec3c44",
						"fb124cf9-e058-4f34-8385-ad225ff85a3",
						"d05087dd-efff-4144-b9a6-89476a14695",
						"5082a8df-cc67-48b4-aca4-26ce1425645",
						"04d9f1b7-f50c-4407-83bb-e9c4ddc5d45",
						"62507779-bd2d-4170-b715-8d93ee7110f",
						"e0dcb798-4f97-4646-a1a9-57a6c69c235",
						"6bfd6b61-f8a9-45b3-9ca8-37125438be4",
						"aecd6aea-1350-46af-a7b9-231e9a609fd",
						"49b67ab9-20fc-42ac-ac53-b36e29834c7",
					},
				},
			},
			want: true,
		},
		{
			name:   "admin key works for evaluation",
			apiKey: "49b67ab9-20fc-42ac-ac53-b36e29834c7",
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"49b67ab9-20fc-42ac-ac53-b36e29834c7",
					},
					Evaluation: []string{
						"xxx",
					},
				},
			},
			want: true,
		},
		{
			name: "no api key passed in the function",
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Evaluation: []string{
						"0359cdb3-5fb5-4d65-b25f-b8909ec3c44",
						"fb124cf9-e058-4f34-8385-ad225ff85a3",
						"d05087dd-efff-4144-b9a6-89476a14695",
						"5082a8df-cc67-48b4-aca4-26ce1425645",
						"04d9f1b7-f50c-4407-83bb-e9c4ddc5d45",
						"62507779-bd2d-4170-b715-8d93ee7110f",
						"e0dcb798-4f97-4646-a1a9-57a6c69c235",
						"6bfd6b61-f8a9-45b3-9ca8-37125438be4",
						"aecd6aea-1350-46af-a7b9-231e9a609fd",
						"49b67ab9-20fc-42ac-ac53-b36e29834c7",
					},
				},
			},
			want: false,
		},
		{
			name:   "empty key passed in the function",
			apiKey: "",
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Evaluation: []string{
						"0359cdb3-5fb5-4d65-b25f-b8909ec3c44",
						"fb124cf9-e058-4f34-8385-ad225ff85a3",
						"d05087dd-efff-4144-b9a6-89476a14695",
						"5082a8df-cc67-48b4-aca4-26ce1425645",
						"04d9f1b7-f50c-4407-83bb-e9c4ddc5d45",
						"62507779-bd2d-4170-b715-8d93ee7110f",
						"e0dcb798-4f97-4646-a1a9-57a6c69c235",
						"6bfd6b61-f8a9-45b3-9ca8-37125438be4",
						"aecd6aea-1350-46af-a7b9-231e9a609fd",
						"49b67ab9-20fc-42ac-ac53-b36e29834c7",
					},
				},
			},
			want: false,
		},
		{
			name:   "key exists in flag set",
			apiKey: "flag-set-api-key",
			config: config.Config{
				FlagSets: []config.FlagSet{
					{
						Name:    "test-flag-set",
						APIKeys: []string{"flag-set-api-key"},
					},
				},
			},
			want: true,
		},
		{
			name:   "key exists in multiple flag sets",
			apiKey: "flag-set-api-key-2",
			config: config.Config{
				FlagSets: []config.FlagSet{
					{
						Name:    "test-flag-set-1",
						APIKeys: []string{"flag-set-api-key-1"},
					},
					{
						Name:    "test-flag-set-2",
						APIKeys: []string{"flag-set-api-key-2"},
					},
				},
			},
			want: true,
		},
		{
			name:   "key does not exist in flag sets",
			apiKey: "non-existent-key",
			config: config.Config{
				FlagSets: []config.FlagSet{
					{
						Name:    "test-flag-set-1",
						APIKeys: []string{"flag-set-api-key-1"},
					},
					{
						Name:    "test-flag-set-2",
						APIKeys: []string{"flag-set-api-key-2"},
					},
				},
			},
			want: false,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.config.ForceReloadAPIKeys()
			assert.Equalf(
				t,
				tt.want,
				tt.config.APIKeyExists(tt.apiKey),
				"APIKeyExists(%v)",
				tt.apiKey,
			)
		})
	}
}

func TestConfig_APIAdminKeyExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config config.Config
		apiKey string
		want   bool
	}{
		{
			name: "no key in the config",
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Admin:      []string{},
					Evaluation: []string{},
				},
			},
			apiKey: "49b67ab9-20fc-42ac-ac53-b36e29834c7",
			want:   false,
		},
		{
			name:   "key exists in a list of keys",
			apiKey: "49b67ab9-20fc-42ac-ac53-b36e29834c7",
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"aecd6aea-1350-46af-a7b9-231e9a609fd",
						"49b67ab9-20fc-42ac-ac53-b36e29834c7",
					},
				},
			},
			want: true,
		},
		{
			name:   "admin key works for evaluation",
			apiKey: "49b67ab9-20fc-42ac-ac53-b36e29834c7",
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"49b67ab9-20fc-42ac-ac53-b36e29834c7",
					},
					Evaluation: []string{
						"xxx",
					},
				},
			},
			want: true,
		},
		{
			name: "no api key passed in the function",
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"49b67ab9-20fc-42ac-ac53-b36e29834c7",
					},
					Evaluation: []string{
						"xxx",
					},
				},
			},
			want: false,
		},
		{
			name:   "empty key passed in the function",
			apiKey: "",
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"49b67ab9-20fc-42ac-ac53-b36e29834c7",
					},
					Evaluation: []string{
						"xxx",
					},
				},
			},
			want: false,
		},
		{
			name:   "evaluation key does not work for admin",
			apiKey: "xxx",
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"49b67ab9-20fc-42ac-ac53-b36e29834c7",
					},
					Evaluation: []string{
						"xxx",
					},
				},
			},
			want: false,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.config.ForceReloadAPIKeys()
			assert.Equalf(
				t,
				tt.want,
				tt.config.APIKeysAdminExists(tt.apiKey),
				"APIKeyExists(%v)",
				tt.apiKey,
			)
		})
	}
}

func TestMergeConfig_FromOSEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		want         *config.Config
		fileLocation string
		wantErr      assert.ErrorAssertionFunc
		envVars      map[string]string
	}{
		{
			name:         "Valid file",
			fileLocation: "../testdata/config/validate-array-env-file.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      "yaml",
				Notifiers: []config.NotifierConf{
					{
						Kind:        config.WebhookNotifier,
						EndpointURL: "https://xxx.com/notifier",
					},
				},
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: "http",
						URL:  "https://raw.githubusercontent.com/BlackbirdWorks/semaphore/main/cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
						HTTPHeaders: map[string][]string{
							"authorization": {
								"test",
							},
							"token": {"token"},
						},
					},
					{
						Kind: "file",
						Path: "cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
						HTTPHeaders: map[string][]string{
							"token": {
								"11213123",
							},
							"authorization": {
								"test1",
							},
						},
					},
					{
						HTTPHeaders: map[string][]string{
							"authorization": {
								"test1",
							},
							"x-goff-custom": {
								"custom",
							},
						},
					},
				},
				Exporter: &config.ExporterConf{
					Kind: "log",
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version: "1.X.X",
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"apikey3",
					},
					Evaluation: []string{
						"apikey1",
						"apikey2",
					},
				},
				LogLevel:      "info",
				EnableSwagger: true,
			},
			wantErr: assert.NoError,
			envVars: map[string]string{
				"RETRIEVERS_0_HEADERS_AUTHORIZATION": "test",
				"RETRIEVERS_X_HEADERS_AUTHORIZATION": "test",
				"RETRIEVERS_1_HEADERS_AUTHORIZATION": "test1",
				"RETRIEVERS_0_HEADERS_TOKEN":         "token",
				"RETRIEVERS_2_HEADERS_AUTHORIZATION": "test1",
				"RETRIEVERS_2_HEADERS_X-GOFF-CUSTOM": "custom",
				"NOTIFIERS_0_ENDPOINTURL":            "https://xxx.com/notifier",
			},
		},
		{
			name:         "Valid file (with legacy notifier without s)",
			fileLocation: "../testdata/config/validate-array-env-file-legacy.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      "yaml",
				FixNotifiers: []config.NotifierConf{
					{
						Kind:        config.WebhookNotifier,
						EndpointURL: "https://xxx.com/notifier",
					},
				},
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: "http",
						URL:  "https://raw.githubusercontent.com/BlackbirdWorks/semaphore/main/cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
						HTTPHeaders: map[string][]string{
							"authorization": {
								"test",
							},
							"token": {"token"},
						},
					},
					{
						Kind: "file",
						Path: "cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
						HTTPHeaders: map[string][]string{
							"token": {
								"11213123",
							},
							"authorization": {
								"test1",
							},
						},
					},
					{
						HTTPHeaders: map[string][]string{
							"authorization": {
								"test1",
							},
							"x-goff-custom": {
								"custom",
							},
						},
					},
				},
				Exporter: &config.ExporterConf{
					Kind: "log",
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version:       "1.X.X",
				EnableSwagger: true,
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"apikey3",
					},
					Evaluation: []string{
						"apikey1",
						"apikey2",
					},
				},
				LogLevel: "info",
			},
			wantErr: assert.NoError,
			envVars: map[string]string{
				"RETRIEVERS_0_HEADERS_AUTHORIZATION": "test",
				"RETRIEVERS_X_HEADERS_AUTHORIZATION": "test",
				"RETRIEVERS_1_HEADERS_AUTHORIZATION": "test1",
				"RETRIEVERS_0_HEADERS_TOKEN":         "token",
				"RETRIEVERS_2_HEADERS_AUTHORIZATION": "test1",
				"RETRIEVERS_2_HEADERS_X-GOFF-CUSTOM": "custom",
				"NOTIFIER_0_ENDPOINTURL":             "https://xxx.com/notifier",
			},
		},
		{
			name:         "Valid file with prefix",
			fileLocation: "../testdata/config/validate-array-env-file-envprefix.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      "yaml",
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: "http",
						URL:  "https://raw.githubusercontent.com/BlackbirdWorks/semaphore/main/cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
						HTTPHeaders: map[string][]string{
							"authorization": {
								"test",
							},
							"token": {"token"},
						},
					},
					{
						Kind: "file",
						Path: "cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
						HTTPHeaders: map[string][]string{
							"token": {
								"11213123",
							},
							"authorization": {
								"test1",
							},
						},
					},
					{
						HTTPHeaders: map[string][]string{
							"authorization": {
								"test1",
							},
							"x-goff-custom": {
								"custom",
							},
						},
					},
				},
				Exporter: &config.ExporterConf{
					Kind: "log",
				},
				StartWithRetrieverError: false,
				EnvVariablePrefix:       "GOFF_",
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version:       "1.X.X",
				EnableSwagger: true,
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"apikey3",
					},
					Evaluation: []string{
						"apikey1",
						"apikey2",
					},
				},
				LogLevel: "info",
			},
			wantErr: assert.NoError,
			envVars: map[string]string{
				"GOFF_RETRIEVERS_0_HEADERS_AUTHORIZATION": "test",
				"GOFF_RETRIEVERS_X_HEADERS_AUTHORIZATION": "test",
				"GOFF_RETRIEVERS_1_HEADERS_AUTHORIZATION": "test1",
				"GOFF_RETRIEVERS_0_HEADERS_TOKEN":         "token",
				"GOFF_RETRIEVERS_2_HEADERS_AUTHORIZATION": "test1",
				"GOFF_RETRIEVERS_2_HEADERS_X-GOFF-CUSTOM": "custom",
			},
		},
		{
			name:         "Change kafka exporter",
			fileLocation: "../testdata/config/validate-array-env-file.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      "yaml",
				Notifiers: []config.NotifierConf{
					{
						Kind:        config.WebhookNotifier,
						EndpointURL: "http://localhost:8080/webhook",
					},
				},
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: "http",
						URL:  "https://raw.githubusercontent.com/BlackbirdWorks/semaphore/main/cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
					},
					{
						Kind: "file",
						Path: "cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
						HTTPHeaders: map[string][]string{
							"token": {
								"11213123",
							},
						},
					},
				},
				Exporter: &config.ExporterConf{
					Kind: "kafka",
					Kafka: kafkaexporter.Settings{
						Addresses: []string{"localhost:19092", "localhost:19093"},
					},
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				EnableSwagger: true,
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"apikey3",
					},
					Evaluation: []string{
						"apikey1",
						"apikey2",
					},
				},
				Version:  "1.X.X",
				LogLevel: "info",
			},
			wantErr: assert.NoError,
			envVars: map[string]string{
				"EXPORTER_KAFKA_ADDRESSES": "localhost:19092,localhost:19093",
				"EXPORTER_KIND":            "kafka",
			},
		},
		{
			name:         "Change kafka exporters",
			fileLocation: "../testdata/config/valid-env-exporters-kafka.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      "yaml",
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: "http",
						URL:  "https://raw.githubusercontent.com/BlackbirdWorks/semaphore/main/cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
					},
					{
						Kind: "file",
						Path: "cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
						HTTPHeaders: map[string][]string{
							"token": {
								"11213123",
							},
						},
					},
				},
				Exporters: &[]config.ExporterConf{
					{
						Kind: "kafka",
						Kafka: kafkaexporter.Settings{
							Addresses: []string{"localhost:19092", "localhost:19093"},
							Topic:     "svc-goff.evaluation",
						},
					},
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				EnableSwagger: true,
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						"apikey3",
					},
					Evaluation: []string{
						"apikey1",
						"apikey2",
					},
				},
				Version:  "1.X.X",
				LogLevel: "info",
			},
			wantErr: assert.NoError,
			envVars: map[string]string{
				"EXPORTERS_0_KAFKA_ADDRESSES": "localhost:19092,localhost:19093",
			},
		},
		{
			name:         "Valid YAML with OTel config",
			fileLocation: "../testdata/config/valid-otel.yaml",
			want: &config.Config{
				PollingInterval: 60000,
				FileFormat:      "yaml",
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: "file",
						Path: "cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
					},
				},
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				LogLevel: config.DefaultLogLevel,
				Version:  "1.X.X",
				OtelConfig: config.OpenTelemetryConfiguration{
					Exporter: config.OtelExporter{
						Otlp: config.OtelExporterOtlp{
							Endpoint: "http://localhost:4317",
						},
					},
					Resource: config.OtelResource{
						Attributes: map[string]string{
							"foo.bar": "baz",
							"foo.baz": "qux",
							"foo.qux": "quux",
						},
					},
				},
			},
			wantErr: assert.NoError,
			envVars: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4317",
				"OTEL_RESOURCE_ATTRIBUTES":    "foo.baz=qux,foo.qux=quux,ignored.key",
			},
		},
		{
			name:         "Configure flagset with basic environment variables",
			fileLocation: "../testdata/config/validate-array-env-file-flagset.yaml",
			want: &config.Config{
				FileFormat:      "yaml",
				PollingInterval: 60000,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				LogLevel: config.DefaultLogLevel,
				Version:  "1.X.X",
				FlagSets: []config.FlagSet{
					{
						Name:                    "xxx",
						APIKeys:                 []string{"other-api-key", "default-api-key"},
						PollingInterval:         50000,
						FileFormat:              "yaml",
						StartWithRetrieverError: true,
						Notifiers: []config.NotifierConf{
							{
								Kind:        config.WebhookNotifier,
								EndpointURL: "http://localhost:8080/webhook",
							},
						},
						Exporters: &[]config.ExporterConf{
							{
								Kind: "kafka",
								Kafka: kafkaexporter.Settings{
									Addresses: []string{"localhost:19092", "localhost:19093"},
								},
							},
						},
						Retrievers: &[]retrieverconf.RetrieverConf{
							{
								Kind: "file",
								Path: "cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
							},
						},
					},
				},
			},
			wantErr: assert.NoError,
			envVars: map[string]string{
				"FLAGSETS_0_NAME":                        "xxx",
				"FLAGSETS_0_APIKEYS":                     "other-api-key,default-api-key",
				"FLAGSETS_0_FILEFORMAT":                  "yaml",
				"FLAGSETS_0_POLLINGINTERVAL":             "50000",
				"FLAGSETS_0_STARTWITHRETRIEVERERROR":     "true",
				"FLAGSETS_0_NOTIFIERS_0_KIND":            "webhook",
				"FLAGSETS_0_NOTIFIERS_0_ENDPOINTURL":     "http://localhost:8080/webhook",
				"FLAGSETS_0_EXPORTERS_0_KIND":            "kafka",
				"FLAGSETS_0_EXPORTERS_0_KAFKA_ADDRESSES": "localhost:19092,localhost:19093",
				"FLAGSETS_0_RETRIEVERS_0_KIND":           "file",
				"FLAGSETS_0_RETRIEVERS_0_PATH":           "cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			environ := make([]string, 0, len(tt.envVars))
			for k, v := range tt.envVars {
				environ = append(environ, k+"="+v)
			}

			f := pflag.NewFlagSet("config", pflag.ContinueOnError)
			f.String("config", "", "Location of your config file")
			_ = f.Parse([]string{fmt.Sprintf("--config=%s", tt.fileLocation)})
			got, err := config.New(f, zap.L(), "1.X.X", withEnviron(environ...))
			if !tt.wantErr(t, err) {
				return
			}
			assert.True(t, cmp.Equal(tt.want, got, cmpopts.IgnoreUnexported(config.Config{})))
		})
	}
}

// The variables are given to the loader (withEnviron), the process environment is not modified.
func TestSetAPIKeysFromEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		envVars        map[string]string
		wantEvaluation []string
		wantAdmin      []string
	}{
		{
			name: "authorized keys from the environment",
			envVars: map[string]string{
				"AUTHORIZEDKEYS_EVALUATION": "key1,key2,key 3",
				"AUTHORIZEDKEYS_ADMIN":      "key4,key5",
			},
			wantEvaluation: []string{"key1", "key2", "key 3"},
			wantAdmin:      []string{"key4", "key5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			environ := make([]string, 0, len(tt.envVars))
			for k, v := range tt.envVars {
				environ = append(environ, k+"="+v)
			}
			f := pflag.NewFlagSet("config", pflag.ContinueOnError)
			f.String("config", "", "Location of your config file")
			_ = f.Parse([]string{"--config=../testdata/config/valid-file.yaml"})

			got, err := config.New(f, zap.L(), "1.X.X", withEnviron(environ...))
			require.NoError(t, err)
			assert.Equal(t, tt.wantEvaluation, got.AuthorizedKeys.Evaluation)
			assert.Equal(t, tt.wantAdmin, got.AuthorizedKeys.Admin)
		})
	}
}

func TestConfig_LogLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		config       *config.Config
		wantDebug    bool
		wantLogLevel zapcore.Level
	}{
		{
			name:         "no config",
			wantDebug:    false,
			wantLogLevel: zapcore.InvalidLevel,
		},
		{
			name: "invalid log level",
			config: &config.Config{
				LogLevel: "invalid",
			},
			wantDebug:    false,
			wantLogLevel: zapcore.InvalidLevel,
		},
		{
			name: "debug level",
			config: &config.Config{
				LogLevel: "debug",
			},
			wantDebug:    true,
			wantLogLevel: zapcore.DebugLevel,
		},
		{
			name: "info level",
			config: &config.Config{
				LogLevel: "info",
			},
			wantDebug:    false,
			wantLogLevel: zapcore.InfoLevel,
		},
		{
			name: "error level",
			config: &config.Config{
				LogLevel: "error",
			},
			wantDebug:    false,
			wantLogLevel: zapcore.ErrorLevel,
		},
		{
			name: "panic level",
			config: &config.Config{
				LogLevel: "panic",
			},
			wantDebug:    false,
			wantLogLevel: zapcore.PanicLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equalf(t, tt.wantDebug, tt.config.IsDebugEnabled(), "IsDebugEnabled()")
			assert.Equalf(t, tt.wantLogLevel, tt.config.ZapLogLevel(), "ZapLogLevel()")
		})
	}
}

func TestConfig_IsDebugEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{
			name: "Uppercase",
			cfg: config.Config{
				LogLevel: "DEBUG",
			},
			want: true,
		},
		{
			name: "Lowercase",
			cfg: config.Config{
				LogLevel: "debug",
			},
			want: true,
		},
		{
			name: "Random Case",
			cfg: config.Config{
				LogLevel: "DeBuG",
			},
			want: true,
		},
		{
			name: "Not debug",
			cfg: config.Config{
				LogLevel: "DeBu",
			},
			want: false,
		},
		{
			name: "Empty",
			cfg: config.Config{
				LogLevel: "",
			},
			want: false,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equalf(t, tt.want, tt.cfg.IsDebugEnabled(), "IsDebugEnabled()")
		})
	}
}

func TestConfigOnlyEnvVars(t *testing.T) {
	t.Parallel()

	t.Run("Retrievers", func(t *testing.T) {
		t.Parallel()

		f := pflag.NewFlagSet("config", pflag.ContinueOnError)

		got, err := config.New(f, zap.L(), "1.X.X", withEnviron(
			"RETRIEVERS_0_KIND="+string(retrieverconf.FileRetriever),
			"RETRIEVERS_0_PATH=cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
		))
		require.NoError(t, err)

		assert.Equal(t, retrieverconf.FileRetriever, (*got.Retrievers)[0].Kind)
		assert.Equal(t, "cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml", (*got.Retrievers)[0].Path)
	})

	t.Run("Flagset retriever", func(t *testing.T) {
		t.Parallel()

		f := pflag.NewFlagSet("config", pflag.ContinueOnError)
		got, err := config.New(f, zap.L(), "1.X.X", withEnviron(
			"FLAGSETS_0_RETRIEVERS_0_KIND="+string(retrieverconf.HTTPRetriever),
			"FLAGSETS_0_RETRIEVERS_0_URL=https://example.com/flags.goff.yaml",
		))
		require.NoError(t, err)

		assert.Equal(t, retrieverconf.HTTPRetriever, (*got.FlagSets[0].Retrievers)[0].Kind)
		assert.Equal(t, "https://example.com/flags.goff.yaml", (*got.FlagSets[0].Retrievers)[0].URL)
	})
}

func TestConfig_IsValid_EnableSharedPollerPerBucket(t *testing.T) {
	t.Parallel()
	s3Item := &retrieverconf.RetrieverConf{Kind: retrieverconf.S3Retriever, Bucket: "flags", Item: "a/flags.yaml"}
	flagset := func(common config.CommonFlagSet) *config.Config {
		common.EnableSharedPollerPerBucket = true

		return &config.Config{
			Server:   config.Server{Port: 8080},
			FlagSets: []config.FlagSet{{Name: "fs", APIKeys: []string{"k"}, CommonFlagSet: common}},
		}
	}
	tests := []struct {
		name    string
		cfg     *config.Config
		wantErr string
	}{
		{name: "s3 with item", cfg: flagset(config.CommonFlagSet{Retriever: s3Item})},
		{
			name: "default (non flagset) config",
			cfg: &config.Config{
				Server:    config.Server{Port: 8080},
				Retriever: s3Item, EnableSharedPollerPerBucket: true,
			},
			wantErr: "only available inside a flagset",
		},
		{
			name: "retrievers",
			cfg: flagset(config.CommonFlagSet{
				Retrievers: &[]retrieverconf.RetrieverConf{*s3Item},
			}),
			wantErr: "can't be used with retrievers",
		},
		{
			name: "retriever and retrievers",
			cfg: flagset(config.CommonFlagSet{
				Retriever:  s3Item,
				Retrievers: &[]retrieverconf.RetrieverConf{*s3Item},
			}),
			wantErr: "can't be used with retrievers",
		},
		{
			name: "other kind",
			cfg: flagset(config.CommonFlagSet{
				Retriever: &retrieverconf.RetrieverConf{Kind: retrieverconf.HTTPRetriever, URL: "http://x"},
			}),
			wantErr: "only supported with a retriever of kind s3",
		},
		{
			name: "folder mode",
			cfg: flagset(config.CommonFlagSet{
				Retriever: &retrieverconf.RetrieverConf{Kind: retrieverconf.S3Retriever, Bucket: "flags", Prefix: "a/"},
			}),
			wantErr: "folder mode is not supported",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.cfg.IsValid()
			if tt.wantErr == "" {
				assert.NoError(t, err)

				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// withEnviron makes the loader read the given "KEY=value" variables instead of the process
// environment, so the tests that load the configuration from the environment can run in parallel.
func withEnviron(environ ...string) config.LoaderOption {
	return config.WithEnviron(func() []string { return environ })
}

func parseCleanQueueConfig(t *testing.T, content string, opts ...config.LoaderOption) *config.Config {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "semaphore-proxy.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0o600))
	flagSet := pflag.NewFlagSet("config", pflag.ContinueOnError)
	flagSet.String("config", "", "Location of your config file")
	require.NoError(t, flagSet.Parse([]string{"--config=" + configPath}))
	got, err := config.New(flagSet, zap.NewNop(), "1.X.X", opts...)
	require.NoError(t, err)

	return got
}

func TestParseConfig_ExporterCleanQueueInterval(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		content      string
		want         time.Duration
		wantFlagSets []time.Duration
	}{
		{name: "unset keeps the zero value", content: "logLevel: info\n", want: 0},
		{name: "set in the config file", content: "exporterCleanQueueInterval: 30s\n", want: 30 * time.Second},
		{
			name: "set at flagset level",
			content: "exporterCleanQueueInterval: 30s\nflagsets:\n" +
				"  - name: a\n    apiKeys: [key-a]\n    exporterCleanQueueInterval: 5s\n" +
				"  - name: b\n    apiKeys: [key-b]\n",
			want:         30 * time.Second,
			wantFlagSets: []time.Duration{5 * time.Second, 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseCleanQueueConfig(t, tt.content)
			assert.Equal(t, tt.want, got.ExporterCleanQueueInterval)
			for i, want := range tt.wantFlagSets {
				assert.Equal(t, want, got.FlagSets[i].ExporterCleanQueueInterval)
			}
		})
	}
}

func TestParseConfig_ExporterCleanQueueInterval_Environment(t *testing.T) {
	t.Parallel()

	got := parseCleanQueueConfig(t, "logLevel: info\n", withEnviron("EXPORTERCLEANQUEUEINTERVAL=2m"))
	assert.Equal(t, 2*time.Minute, got.ExporterCleanQueueInterval)
}

func TestConfig_IsValid_ExporterCleanQueueInterval(t *testing.T) {
	t.Parallel()
	retriever := &retrieverconf.RetrieverConf{Kind: retrieverconf.HTTPRetriever, URL: "https://example.com/flags.yaml"}
	tests := []struct {
		name    string
		cfg     *config.Config
		wantErr string
	}{
		{name: "unset", cfg: &config.Config{Retriever: retriever}},
		{
			name: "positive",
			cfg: &config.Config{
				Retriever: retriever, ExporterCleanQueueInterval: time.Second},
		},
		{
			name: "negative",
			cfg: &config.Config{
				Retriever: retriever, ExporterCleanQueueInterval: -time.Second},
			wantErr: "exporterCleanQueueInterval must not be negative",
		},
		{
			name: "negative in a flagset",
			cfg: &config.Config{FlagSets: []config.FlagSet{{
				Name: "a", APIKeys: []string{"k"},
				Retriever: retriever, ExporterCleanQueueInterval: -time.Second,
			}}},
			wantErr: "flagset a: exporterCleanQueueInterval must not be negative",
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.cfg.IsValid()
			if tt.wantErr == "" {
				assert.NoError(t, err)

				return
			}
			assert.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestConfig_OtelResourceAttributesDoNotLeakValues(t *testing.T) {
	t.Parallel()

	const marker = "SECRETMARKER-7f3a"
	tests := []struct {
		name         string
		attributes   string
		wantPosition int64
	}{
		{name: "malformed entry in last position", attributes: "foo=bar,Bearer-" + marker, wantPosition: 1},
		{name: "malformed entry in first position", attributes: "token" + marker + ",foo=bar", wantPosition: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			core, logs := observer.New(zapcore.DebugLevel)

			_, err := config.New(pflag.NewFlagSet("config", pflag.ContinueOnError), zap.New(core), "1.X.X",
				withEnviron("OTEL_RESOURCE_ATTRIBUTES="+tt.attributes))
			require.NoError(t, err)

			entries := logs.FilterMessageSnippet("OTEL_RESOURCE_ATTRIBUTES").All()
			require.Len(t, entries, 1)
			assert.Equal(t, zapcore.ErrorLevel, entries[0].Level)
			assert.Equal(t, tt.wantPosition, entries[0].ContextMap()["position"])
			for _, e := range logs.All() {
				assert.NotContains(t, fmt.Sprintf("%v %v", e.Message, e.ContextMap()), marker)
			}
		})
	}
}
