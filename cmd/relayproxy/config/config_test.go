package config_test

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/exporter/kafkaexporter"
	"github.com/blackbirdworks/semaphore/testhelpers/slogutil"
)

const (
	aecd6Aea1350                   = "aecd6aea-1350-46af-a7b9-231e9a609fd"
	ampleFlagsGoffYaml             = "ample/flags.goff.yaml"
	apikey12                       = "apikey1"
	apikey22                       = "apikey2"
	apikey32                       = "apikey3"
	authorization2                 = "authorization"
	cmdRelayproxyTestdataDockerhub = "cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml"
	config3                        = "config"
	configS                        = "--config=%s"
	custom2                        = "custom"
	d05087DdEfff                   = "d05087dd-efff-4144-b9a6-89476a14695"
	debug                          = "debug"
	e0Dcb798                       = "e0dcb798-4f97-4646-a1a9-57a6c69c235"
	extraInfo2                     = "extraInfo"
	fb124Cf9                       = "fb124cf9-e058-4f34-8385-ad225ff85a3"
	file2                          = "file"
	apiKeyFirst                    = "0359cdb3-5fb5-4d65-b25f-b8909ec3c44"
	apiKeyThird                    = "04d9f1b7-f50c-4407-83bb-e9c4ddc5d45"
	numericAPIKey                  = "11213123"
	wildcardVersion                = "1.X.X"
	apiKeySixth                    = "49b67ab9-20fc-42ac-ac53-b36e29834c7"
	apiKeySecond                   = "5082a8df-cc67-48b4-aca4-26ce1425645"
	apiKeyFourth                   = "62507779-bd2d-4170-b715-8d93ee7110f"
	apiKeyFifth                    = "6bfd6b61-f8a9-45b3-9ca8-37125438be4"
	flagSetAPIKey                  = "flag-set-api-key-2"
	flagset1                       = "flagset-1"
	flagset2                       = "flagset-2"
	http2                          = "http"
	httpLocalhost8080Webhook       = "http://localhost:8080/webhook"
	httpTestingwebhookComTest      = "http://testingwebhook.com/test/"
	httpsHooksSlackCom             = "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX"
	httpsHooktestCom               = "https://hooktest.com/"
	httpsRawGithubusercontentCom   = "https://raw.githubusercontent.com/BlackbirdWorks/s" +
		"emaphore/main/cmd/relayproxy/testdata/dockerhub-ex"
	httpsRawGithubusercontentCom2 = "https://raw.githubusercontent.com/BlackbirdWorks/s" +
		"emaphore/main/cmd/relayproxy/testdata/dockerhub-exa"
	httpsXxxComNotifier          = "https://xxx.com/notifier"
	info2                        = "info"
	invalid2                     = "invalid"
	kafka                        = "kafka"
	localhost19092               = "localhost:19092"
	localhost19092Localhost19093 = "localhost:19092,localhost:19093"
	localhost19093               = "localhost:19093"
	locationOfYourConfig         = "Location of your config file"
	log2                         = "log"
	mpleFlagsGoffYaml            = "mple/flags.goff.yaml"
	secretForSigning             = "secret-for-signing"
	slack                        = "slack"
	test12                       = "test1"
	test2                        = "test"
	testAPIKey                   = "test-api-key"
	testFlagset                  = "test-flagset"
	testdataConfigValidFile      = "../testdata/config/valid-file.yaml"
	token2                       = "token"
	webhook                      = "webhook"
	xGoffCustom                  = "x-goff-custom"
	xxx2                         = "xxx"
	xxxx                         = "xxxx"
	yaml2                        = "yaml"
)

func TestParseConfigDisableFlagDetailsInStream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		content      string
		environment  string
		wantDisabled bool
	}{
		{name: "disabled by config file", content: "disableFlagDetailsInStream: true\n", wantDisabled: true},
		{name: "disabled by environment variable", environment: "true", wantDisabled: true},
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

			flagSet := pflag.NewFlagSet(config3, pflag.ContinueOnError)
			flagSet.String(config3, "", locationOfYourConfig)
			require.NoError(t, flagSet.Parse([]string{config2 + configPath}))

			got, err := config.New(flagSet, slog.New(slog.DiscardHandler), wildcardVersion, withEnviron(environ...))
			require.NoError(t, err)
			assert.Equal(t, tt.wantDisabled, got.DisableFlagDetailsInStream)
		})
	}
}

type parseConfigFileCase struct {
	want         *config.Config
	wantErr      assert.ErrorAssertionFunc
	name         string
	fileLocation string
}

func runParseConfigFromPflag(t *testing.T, tests []parseConfigFileCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := pflag.NewFlagSet(config3, pflag.ContinueOnError)
			f.String(config3, "", locationOfYourConfig)
			_ = f.Parse([]string{fmt.Sprintf(configS, tt.fileLocation)})

			got, err := config.New(f, slog.New(slog.DiscardHandler), wildcardVersion, withEnviron("version=1.X.X"))
			if !tt.wantErr(t, err) {
				return
			}

			assert.True(t, cmp.Equal(tt.want, got, cmpopts.IgnoreUnexported(config.Config{})))
		})
	}
}

func TestParseConfig_fileFromPflag(t *testing.T) {
	t.Parallel()

	runParseConfigFromPflag(t, []parseConfigFileCase{
		{
			name:         "Valid yaml file",
			fileLocation: testdataConfigValidFile,
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      yaml2,
				Retriever: &retrieverconf.RetrieverConf{
					Kind: http2,
					URL: httpsRawGithubusercontentCom2 +
						mpleFlagsGoffYaml,
				},
				Exporter: &config.ExporterConf{
					Kind: log2,
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Swagger: config.Swagger{Enabled: true, Host: "gofeatureflag.org"},
				Version: wildcardVersion,
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						apikey32,
					},
					Evaluation: []string{
						apikey12,
						apikey22,
					},
				},
				LogLevel: info2,
			},
			wantErr: assert.NoError,
		},
		{
			name:         "Valid yaml file with legacy key notifier (without s)",
			fileLocation: "../testdata/config/valid-file-notifier.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      yaml2,
				Retriever: &retrieverconf.RetrieverConf{
					Kind: http2,
					URL: httpsRawGithubusercontentCom2 +
						mpleFlagsGoffYaml,
				},
				Exporter: &config.ExporterConf{
					Kind: log2,
				},
				StartWithRetrieverError: false,
				FixNotifiers: []config.NotifierConf{
					{Kind: config.DiscordNotifier, WebhookURL: "https://discord.com/api/webhooks/yyyy/xxxxxxx"},
				},
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version:       wildcardVersion,
				EnableSwagger: true,
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						apikey32,
					},
					Evaluation: []string{
						apikey12,
						apikey22,
					},
				},

				LogLevel: info2,
			},
			wantErr: assert.NoError,
		},
		{
			name:         "Valid yaml file with legacy key notifier (without s)",
			fileLocation: "../testdata/config/valid-file-notifiers.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      yaml2,
				Retriever: &retrieverconf.RetrieverConf{
					Kind: http2,
					URL: httpsRawGithubusercontentCom2 +
						mpleFlagsGoffYaml,
				},
				Exporter: &config.ExporterConf{
					Kind: log2,
				},
				StartWithRetrieverError: false,
				Notifiers: []config.NotifierConf{
					{Kind: config.DiscordNotifier, WebhookURL: "https://discord.com/api/webhooks/yyyy/xxxxxxx"},
				},
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version:       wildcardVersion,
				EnableSwagger: true,
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						apikey32,
					},
					Evaluation: []string{
						apikey12,
						apikey22,
					},
				},

				LogLevel: info2,
			},
			wantErr: assert.NoError,
		},
		{
			name:         "Valid yaml file with multiple exporters",
			fileLocation: "../testdata/config/valid-yaml-multiple-exporters.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      yaml2,
				Retriever: &retrieverconf.RetrieverConf{
					Kind: http2,
					URL: httpsRawGithubusercontentCom2 +
						mpleFlagsGoffYaml,
				},
				Exporters: &[]config.ExporterConf{
					{
						Kind: log2,
					},
					{
						Kind:      file2,
						OutputDir: "./",
					},
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version:       wildcardVersion,
				EnableSwagger: true,
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						apikey32,
					},
					Evaluation: []string{
						apikey12,
						apikey22,
					},
				},
				LogLevel: info2,
			},
			wantErr: assert.NoError,
		},
		{
			name:         "Valid yaml file with both exporter and exporters",
			fileLocation: "../testdata/config/valid-yaml-exporter-and-exporters.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      yaml2,
				Retriever: &retrieverconf.RetrieverConf{
					Kind: http2,
					URL: httpsRawGithubusercontentCom2 +
						mpleFlagsGoffYaml,
				},
				Exporter: &config.ExporterConf{
					Kind: log2,
				},
				Exporters: &[]config.ExporterConf{
					{
						Kind:        webhook,
						EndpointURL: "https://example.com/webhook",
					},
					{
						Kind:      file2,
						OutputDir: "./",
					},
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version:       wildcardVersion,
				EnableSwagger: true,
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						apikey32,
					},
					Evaluation: []string{
						apikey12,
						apikey22,
					},
				},
				LogLevel: info2,
			},
			wantErr: assert.NoError,
		},
	})
}

func TestParseConfig_fileFromPflag_FormatsAndErrors(t *testing.T) {
	t.Parallel()

	runParseConfigFromPflag(t, []parseConfigFileCase{
		{
			name:         "Valid json file",
			fileLocation: "../testdata/config/valid-file.json",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      yaml2,
				Retriever: &retrieverconf.RetrieverConf{
					Kind: http2,
					URL: httpsRawGithubusercontentCom2 +
						mpleFlagsGoffYaml,
				},
				Exporter: &config.ExporterConf{
					Kind: log2,
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version:       wildcardVersion,
				EnableSwagger: true,
				AuthorizedKeys: config.APIKeys{
					Evaluation: []string{
						apikey12,
						apikey22,
					},
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
				FileFormat:      yaml2,
				Retriever: &retrieverconf.RetrieverConf{
					Kind: http2,
					URL: httpsRawGithubusercontentCom2 +
						mpleFlagsGoffYaml,
				},
				Exporter: &config.ExporterConf{
					Kind: log2,
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version:       wildcardVersion,
				EnableSwagger: true,
				AuthorizedKeys: config.APIKeys{
					Evaluation: []string{
						apikey12,
						apikey22,
					},
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
				FileFormat:              yaml2,
				StartWithRetrieverError: false,
				Version:                 wildcardVersion,
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
				FileFormat:      yaml2,
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: file2,
						Path: cmdRelayproxyTestdataDockerhub,
					},
				},
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				LogLevel: config.DefaultLogLevel,
				Version:  wildcardVersion,
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
	})
}

func TestParseConfig_fileFromFolder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want         *config.Config
		wantErr      assert.ErrorAssertionFunc
		name         string
		fileLocation string
	}{
		{
			name:         "Valid file",
			fileLocation: testdataConfigValidFile,
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      yaml2,
				Retriever: &retrieverconf.RetrieverConf{
					Kind: http2,
					URL: httpsRawGithubusercontentCom2 +
						mpleFlagsGoffYaml,
				},
				Exporter: &config.ExporterConf{
					Kind: log2,
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version: wildcardVersion,
				Swagger: config.Swagger{Enabled: true, Host: "gofeatureflag.org"},
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						apikey32,
					},
					Evaluation: []string{
						apikey12,
						apikey22,
					},
				},
				LogLevel: info2,
			},
			wantErr: assert.NoError,
		},
		{
			name:         "All default",
			fileLocation: "../testdata/config/all-default.yaml",
			want: &config.Config{
				PollingInterval:         60000,
				FileFormat:              yaml2,
				StartWithRetrieverError: false,
				Version:                 wildcardVersion,
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
				FileFormat:              yaml2,
				StartWithRetrieverError: false,
				Version:                 wildcardVersion,
				LogLevel:                config.DefaultLogLevel,
			},
		},
		{
			name:         "Should return all default if no file in the command line",
			fileLocation: "",
			wantErr:      assert.NoError,
			want: &config.Config{
				PollingInterval:         60000,
				FileFormat:              yaml2,
				StartWithRetrieverError: false,
				Version:                 wildcardVersion,
				LogLevel:                config.DefaultLogLevel,
			},
		},
		{
			name:         "Should return all default if no file and no default",
			fileLocation: "",
			wantErr:      assert.NoError,
			want: &config.Config{
				PollingInterval:         60000,
				FileFormat:              yaml2,
				StartWithRetrieverError: false,
				Version:                 wildcardVersion,
				LogLevel:                config.DefaultLogLevel,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := pflag.NewFlagSet(config3, pflag.ContinueOnError)
			f.String(config3, "", locationOfYourConfig)
			_ = f.Parse([]string{fmt.Sprintf(configS, tt.fileLocation)})

			got, err := config.New(f, slog.New(slog.DiscardHandler), wildcardVersion)
			if !tt.wantErr(t, err) {
				return
			}

			assert.True(t, cmp.Equal(tt.want, got, cmpopts.IgnoreUnexported(config.Config{})))
		})
	}
}

type configIsValidFields struct {
	Retriever               *retrieverconf.RetrieverConf
	Retrievers              *[]retrieverconf.RetrieverConf
	Exporter                *config.ExporterConf
	LogLevel                string
	FileFormat              string
	LogFormat               string
	FlagSets                []config.FlagSet
	Swagger                 config.Swagger
	Notifiers               []config.NotifierConf
	OfrepEventStream        config.OfrepEventStream
	Server                  config.Server
	PollingInterval         int
	StartWithRetrieverError bool
	Debug                   bool
	HideBanner              bool
}

type configIsValidCase struct {
	wantErr assert.ErrorAssertionFunc
	name    string
	fields  configIsValidFields
}

func runConfigIsValidTests(t *testing.T, tests []configIsValidCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &config.Config{
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
				cfg = nil
			}

			tt.wantErr(t, cfg.IsValid(), "invalid configuration")
		})
	}
}

func TestConfig_IsValid(t *testing.T) {
	t.Parallel()

	runConfigIsValidTests(t, []configIsValidCase{
		{
			name: "invalid ofrepEventStream",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: file2,
					Path: testdataConfigValidFile,
				},
				OfrepEventStream: config.OfrepEventStream{BaseURL: "gofeatureflag.example.com"},
			},
			wantErr: assert.Error,
		},
		{
			name:    "empty config",
			fields:  configIsValidFields{},
			wantErr: assert.Error,
		},
		{
			name:    "invalid port",
			fields:  configIsValidFields{},
			wantErr: assert.Error,
		},
		{
			name: "no retriever",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Notifiers: []config.NotifierConf{
					{
						Kind:        webhook,
						EndpointURL: httpsHooktestCom,
						Secret:      xxxx,
					},
					{
						Kind:       slack,
						WebhookURL: httpsHooksSlackCom,
					},
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "valid configuration",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: file2,
					Path: testdataConfigValidFile,
				},
				Exporter: &config.ExporterConf{
					Kind:        webhook,
					EndpointURL: httpTestingwebhookComTest,
					Secret:      secretForSigning,
					Meta: map[string]string{
						extraInfo2: info2,
					},
				},
				Notifiers: []config.NotifierConf{
					{
						Kind:        webhook,
						EndpointURL: httpsHooktestCom,
						Secret:      xxxx,
					},
					{
						Kind:       slack,
						WebhookURL: httpsHooksSlackCom,
					},
				},
				LogLevel: info2,
			},
			wantErr: assert.NoError,
		},
		{
			name: "valid configuration with notifier included",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: file2,
					Path: "../testdata/config/valid-file-notifier.yaml",
				},
				Exporter: &config.ExporterConf{
					Kind:        webhook,
					EndpointURL: httpTestingwebhookComTest,
					Secret:      secretForSigning,
					Meta: map[string]string{
						extraInfo2: info2,
					},
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "invalid retriever",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: file2,
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "1 invalid retriever in the list of retrievers",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: file2,
						Path: testdataConfigValidFile,
					},
					{
						Kind: file2,
					},
					{
						Kind: file2,
						Path: testdataConfigValidFile,
					},
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "invalid exporter",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: file2,
					Path: testdataConfigValidFile,
				},
				Exporter: &config.ExporterConf{
					Kind: webhook,
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "invalid notifier",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: file2,
					Path: testdataConfigValidFile,
				},
				Notifiers: []config.NotifierConf{
					{
						Kind: webhook,
					},
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "invalid log level",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: file2,
					Path: testdataConfigValidFile,
				},
				LogLevel: invalid2,
			},
			wantErr: assert.Error,
		},
		{
			name: "mixed case log level",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: file2,
					Path: testdataConfigValidFile,
				},
				LogLevel: "DeBuG",
			},
			wantErr: assert.NoError,
		},
		{
			name: "warning alias log level",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: file2,
					Path: testdataConfigValidFile,
				},
				LogLevel: "warning",
			},
			wantErr: assert.NoError,
		},
		{
			name: "panic log level",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: file2,
					Path: testdataConfigValidFile,
				},
				LogLevel: "panic",
			},
			wantErr: assert.NoError,
		},
		{
			name: "dpanic log level",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: file2,
					Path: testdataConfigValidFile,
				},
				LogLevel: "DPANIC",
			},
			wantErr: assert.NoError,
		},
		{
			name: "log level is not set but debug is set",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: file2,
					Path: testdataConfigValidFile,
				},
				LogLevel: "",
				Debug:    true,
			},
			wantErr: assert.NoError,
		},
		{
			name: "invalid logFormat",
			fields: configIsValidFields{
				LogFormat: "unknown",
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: file2,
					Path: testdataConfigValidFile,
				},
				LogLevel: info2,
			},
			wantErr: assert.Error,
		},
	})
}

func TestConfig_IsValid_FlagSets(t *testing.T) {
	t.Parallel()

	runConfigIsValidTests(t, []configIsValidCase{
		{
			name: "valid flagset configuration",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				FlagSets: []config.FlagSet{
					{
						Name:    testFlagset,
						APIKeys: []string{testAPIKey},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: file2,
							Path: testdataConfigValidFile,
						},
					},
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "flagset without name",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				FlagSets: []config.FlagSet{
					{
						APIKeys: []string{testAPIKey},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: file2,
							Path: testdataConfigValidFile,
						},
					},
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "flagset without API keys",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				FlagSets: []config.FlagSet{
					{
						Name: testFlagset,
						Retriever: &retrieverconf.RetrieverConf{
							Kind: file2,
							Path: testdataConfigValidFile,
						},
					},
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "duplicate API keys across flagsets",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				FlagSets: []config.FlagSet{
					{
						Name:    flagset1,
						APIKeys: []string{"shared-api-key"},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: file2,
							Path: testdataConfigValidFile,
						},
					},
					{
						Name:    flagset2,
						APIKeys: []string{"shared-api-key"},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: file2,
							Path: testdataConfigValidFile,
						},
					},
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "flagset with invalid retriever",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				FlagSets: []config.FlagSet{
					{
						Name:    testFlagset,
						APIKeys: []string{testAPIKey},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: file2,
							// Missing Path
						},
					},
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "flagset with invalid exporter",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				FlagSets: []config.FlagSet{
					{
						Name:    testFlagset,
						APIKeys: []string{testAPIKey},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: file2,
							Path: testdataConfigValidFile,
						},
						Exporter: &config.ExporterConf{
							Kind: webhook,
							// Missing EndpointURL
						},
					},
				},
			},
			wantErr: assert.Error,
		},
		{
			name: "flagset with invalid notifier",
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				FlagSets: []config.FlagSet{
					{
						Name:    testFlagset,
						APIKeys: []string{testAPIKey},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: file2,
							Path: testdataConfigValidFile,
						},
						Notifiers: []config.NotifierConf{
							{
								Kind: webhook,
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
			fields: configIsValidFields{
				Server: config.Server{
					Port: 8080,
					Mode: config.ServerModeHTTP,
				},
				FlagSets: []config.FlagSet{
					{
						Name:    flagset1,
						APIKeys: []string{"api-key-1"},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: file2,
							Path: testdataConfigValidFile,
						},
					},
					{
						Name:    flagset2,
						APIKeys: []string{"api-key-2"},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: file2,
							Path: testdataConfigValidFile,
						},
					},
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "missing unix socket path",
			fields: configIsValidFields{
				LogFormat: "unknown",
				Server: config.Server{
					Mode:           config.ServerModeUnixSocket,
					UnixSocketPath: "",
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: file2,
					Path: testdataConfigValidFile,
				},
				LogLevel: info2,
			},
			wantErr: assert.Error,
		},
	})
}

func TestConfig_APIKeyExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		apiKey string
		config config.Config
		want   bool
	}{
		{
			name: "no key in the config",
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Evaluation: []string{},
				},
			},
			apiKey: apiKeySixth,
			want:   false,
		},
		{
			name:   "key exists in a list of keys (legacy)",
			apiKey: apiKeySixth,
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Evaluation: []string{
						apiKeyFirst,
						fb124Cf9,
						d05087DdEfff,
						apiKeySecond,
						apiKeyThird,
						apiKeyFourth,
						e0Dcb798,
						apiKeyFifth,
						aecd6Aea1350,
						apiKeySixth,
					},
				},
			},
			want: true,
		},
		{
			name:   "key exists in a list of keys",
			apiKey: apiKeySixth,
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Evaluation: []string{
						apiKeyFirst,
						fb124Cf9,
						d05087DdEfff,
						apiKeySecond,
						apiKeyThird,
						apiKeyFourth,
						e0Dcb798,
						apiKeyFifth,
						aecd6Aea1350,
						apiKeySixth,
					},
				},
			},
			want: true,
		},
		{
			name:   "admin key works for evaluation",
			apiKey: apiKeySixth,
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						apiKeySixth,
					},
					Evaluation: []string{
						xxx2,
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
						apiKeyFirst,
						fb124Cf9,
						d05087DdEfff,
						apiKeySecond,
						apiKeyThird,
						apiKeyFourth,
						e0Dcb798,
						apiKeyFifth,
						aecd6Aea1350,
						apiKeySixth,
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
						apiKeyFirst,
						fb124Cf9,
						d05087DdEfff,
						apiKeySecond,
						apiKeyThird,
						apiKeyFourth,
						e0Dcb798,
						apiKeyFifth,
						aecd6Aea1350,
						apiKeySixth,
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
			apiKey: flagSetAPIKey,
			config: config.Config{
				FlagSets: []config.FlagSet{
					{
						Name:    "test-flag-set-1",
						APIKeys: []string{"flag-set-api-key-1"},
					},
					{
						Name:    "test-flag-set-2",
						APIKeys: []string{flagSetAPIKey},
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
						APIKeys: []string{flagSetAPIKey},
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
		apiKey string
		config config.Config
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
			apiKey: apiKeySixth,
			want:   false,
		},
		{
			name:   "key exists in a list of keys",
			apiKey: apiKeySixth,
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						aecd6Aea1350,
						apiKeySixth,
					},
				},
			},
			want: true,
		},
		{
			name:   "admin key works for evaluation",
			apiKey: apiKeySixth,
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						apiKeySixth,
					},
					Evaluation: []string{
						xxx2,
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
						apiKeySixth,
					},
					Evaluation: []string{
						xxx2,
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
						apiKeySixth,
					},
					Evaluation: []string{
						xxx2,
					},
				},
			},
			want: false,
		},
		{
			name:   "evaluation key does not work for admin",
			apiKey: xxx2,
			config: config.Config{
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						apiKeySixth,
					},
					Evaluation: []string{
						xxx2,
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

type mergeConfigFromOSEnvCase struct {
	want         *config.Config
	wantErr      assert.ErrorAssertionFunc
	envVars      map[string]string
	name         string
	fileLocation string
}

func runMergeConfigFromOSEnvTests(t *testing.T, tests []mergeConfigFromOSEnvCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			environ := make([]string, 0, len(tt.envVars))
			for k, v := range tt.envVars {
				environ = append(environ, k+"="+v)
			}

			f := pflag.NewFlagSet(config3, pflag.ContinueOnError)
			f.String(config3, "", locationOfYourConfig)
			_ = f.Parse([]string{fmt.Sprintf(configS, tt.fileLocation)})

			got, err := config.New(f, slog.New(slog.DiscardHandler), wildcardVersion, withEnviron(environ...))
			if !tt.wantErr(t, err) {
				return
			}

			assert.True(t, cmp.Equal(tt.want, got, cmpopts.IgnoreUnexported(config.Config{})))
		})
	}
}

func TestMergeConfig_FromOSEnv(t *testing.T) {
	t.Parallel()

	runMergeConfigFromOSEnvTests(t, []mergeConfigFromOSEnvCase{
		{
			name:         "Valid file",
			fileLocation: "../testdata/config/validate-array-env-file.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      yaml2,
				Notifiers: []config.NotifierConf{
					{
						Kind:        config.WebhookNotifier,
						EndpointURL: httpsXxxComNotifier,
					},
				},
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: http2,
						URL: httpsRawGithubusercontentCom +
							ampleFlagsGoffYaml,
						HTTPHeaders: map[string][]string{
							authorization2: {
								test2,
							},
							token2: {token2},
						},
					},
					{
						Kind: file2,
						Path: cmdRelayproxyTestdataDockerhub,
						HTTPHeaders: map[string][]string{
							token2: {
								numericAPIKey,
							},
							authorization2: {
								test12,
							},
						},
					},
					{
						HTTPHeaders: map[string][]string{
							authorization2: {
								test12,
							},
							xGoffCustom: {
								custom2,
							},
						},
					},
				},
				Exporter: &config.ExporterConf{
					Kind: log2,
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version: wildcardVersion,
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						apikey32,
					},
					Evaluation: []string{
						apikey12,
						apikey22,
					},
				},
				LogLevel:      info2,
				EnableSwagger: true,
			},
			wantErr: assert.NoError,
			envVars: map[string]string{
				"RETRIEVERS_0_HEADERS_AUTHORIZATION": test2,
				"RETRIEVERS_X_HEADERS_AUTHORIZATION": test2,
				"RETRIEVERS_1_HEADERS_AUTHORIZATION": test12,
				"RETRIEVERS_0_HEADERS_TOKEN":         token2,
				"RETRIEVERS_2_HEADERS_AUTHORIZATION": test12,
				"RETRIEVERS_2_HEADERS_X-GOFF-CUSTOM": custom2,
				"NOTIFIERS_0_ENDPOINTURL":            httpsXxxComNotifier,
			},
		},
		{
			name:         "Valid file (with legacy notifier without s)",
			fileLocation: "../testdata/config/validate-array-env-file-legacy.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      yaml2,
				FixNotifiers: []config.NotifierConf{
					{
						Kind:        config.WebhookNotifier,
						EndpointURL: httpsXxxComNotifier,
					},
				},
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: http2,
						URL: httpsRawGithubusercontentCom +
							ampleFlagsGoffYaml,
						HTTPHeaders: map[string][]string{
							authorization2: {
								test2,
							},
							token2: {token2},
						},
					},
					{
						Kind: file2,
						Path: cmdRelayproxyTestdataDockerhub,
						HTTPHeaders: map[string][]string{
							token2: {
								numericAPIKey,
							},
							authorization2: {
								test12,
							},
						},
					},
					{
						HTTPHeaders: map[string][]string{
							authorization2: {
								test12,
							},
							xGoffCustom: {
								custom2,
							},
						},
					},
				},
				Exporter: &config.ExporterConf{
					Kind: log2,
				},
				StartWithRetrieverError: false,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version:       wildcardVersion,
				EnableSwagger: true,
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						apikey32,
					},
					Evaluation: []string{
						apikey12,
						apikey22,
					},
				},
				LogLevel: info2,
			},
			wantErr: assert.NoError,
			envVars: map[string]string{
				"RETRIEVERS_0_HEADERS_AUTHORIZATION": test2,
				"RETRIEVERS_X_HEADERS_AUTHORIZATION": test2,
				"RETRIEVERS_1_HEADERS_AUTHORIZATION": test12,
				"RETRIEVERS_0_HEADERS_TOKEN":         token2,
				"RETRIEVERS_2_HEADERS_AUTHORIZATION": test12,
				"RETRIEVERS_2_HEADERS_X-GOFF-CUSTOM": custom2,
				"NOTIFIER_0_ENDPOINTURL":             httpsXxxComNotifier,
			},
		},
		{
			name:         "Valid file with prefix",
			fileLocation: "../testdata/config/validate-array-env-file-envprefix.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      yaml2,
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: http2,
						URL: httpsRawGithubusercontentCom +
							ampleFlagsGoffYaml,
						HTTPHeaders: map[string][]string{
							authorization2: {
								test2,
							},
							token2: {token2},
						},
					},
					{
						Kind: file2,
						Path: cmdRelayproxyTestdataDockerhub,
						HTTPHeaders: map[string][]string{
							token2: {
								numericAPIKey,
							},
							authorization2: {
								test12,
							},
						},
					},
					{
						HTTPHeaders: map[string][]string{
							authorization2: {
								test12,
							},
							xGoffCustom: {
								custom2,
							},
						},
					},
				},
				Exporter: &config.ExporterConf{
					Kind: log2,
				},
				StartWithRetrieverError: false,
				EnvVariablePrefix:       "GOFF_",
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				Version:       wildcardVersion,
				EnableSwagger: true,
				AuthorizedKeys: config.APIKeys{
					Admin: []string{
						apikey32,
					},
					Evaluation: []string{
						apikey12,
						apikey22,
					},
				},
				LogLevel: info2,
			},
			wantErr: assert.NoError,
			envVars: map[string]string{
				"GOFF_RETRIEVERS_0_HEADERS_AUTHORIZATION": test2,
				"GOFF_RETRIEVERS_X_HEADERS_AUTHORIZATION": test2,
				"GOFF_RETRIEVERS_1_HEADERS_AUTHORIZATION": test12,
				"GOFF_RETRIEVERS_0_HEADERS_TOKEN":         token2,
				"GOFF_RETRIEVERS_2_HEADERS_AUTHORIZATION": test12,
				"GOFF_RETRIEVERS_2_HEADERS_X-GOFF-CUSTOM": custom2,
			},
		},
	})
}

func TestMergeConfig_FromOSEnv_ExportersOTelAndFlagSets(t *testing.T) {
	t.Parallel()

	runMergeConfigFromOSEnvTests(t, []mergeConfigFromOSEnvCase{
		{
			name:         "Change kafka exporter",
			fileLocation: "../testdata/config/validate-array-env-file.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      yaml2,
				Notifiers: []config.NotifierConf{
					{
						Kind:        config.WebhookNotifier,
						EndpointURL: httpLocalhost8080Webhook,
					},
				},
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: http2,
						URL: httpsRawGithubusercontentCom +
							ampleFlagsGoffYaml,
					},
					{
						Kind: file2,
						Path: cmdRelayproxyTestdataDockerhub,
						HTTPHeaders: map[string][]string{
							token2: {
								numericAPIKey,
							},
						},
					},
				},
				Exporter: &config.ExporterConf{
					Kind: kafka,
					Kafka: kafkaexporter.Settings{
						Addresses: []string{localhost19092, localhost19093},
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
						apikey32,
					},
					Evaluation: []string{
						apikey12,
						apikey22,
					},
				},
				Version:  wildcardVersion,
				LogLevel: info2,
			},
			wantErr: assert.NoError,
			envVars: map[string]string{
				"EXPORTER_KAFKA_ADDRESSES": localhost19092Localhost19093,
				"EXPORTER_KIND":            kafka,
			},
		},
		{
			name:         "Change kafka exporters",
			fileLocation: "../testdata/config/valid-env-exporters-kafka.yaml",
			want: &config.Config{
				PollingInterval: 1000,
				FileFormat:      yaml2,
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: http2,
						URL: httpsRawGithubusercontentCom +
							ampleFlagsGoffYaml,
					},
					{
						Kind: file2,
						Path: cmdRelayproxyTestdataDockerhub,
						HTTPHeaders: map[string][]string{
							token2: {
								numericAPIKey,
							},
						},
					},
				},
				Exporters: &[]config.ExporterConf{
					{
						Kind: kafka,
						Kafka: kafkaexporter.Settings{
							Addresses: []string{localhost19092, localhost19093},
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
						apikey32,
					},
					Evaluation: []string{
						apikey12,
						apikey22,
					},
				},
				Version:  wildcardVersion,
				LogLevel: info2,
			},
			wantErr: assert.NoError,
			envVars: map[string]string{
				"EXPORTERS_0_KAFKA_ADDRESSES": localhost19092Localhost19093,
			},
		},
		{
			name:         "Valid YAML with OTel config",
			fileLocation: "../testdata/config/valid-otel.yaml",
			want: &config.Config{
				PollingInterval: 60000,
				FileFormat:      yaml2,
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: file2,
						Path: cmdRelayproxyTestdataDockerhub,
					},
				},
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				LogLevel: config.DefaultLogLevel,
				Version:  wildcardVersion,
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
				FileFormat:      yaml2,
				PollingInterval: 60000,
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: 1031,
				},
				LogLevel: config.DefaultLogLevel,
				Version:  wildcardVersion,
				FlagSets: []config.FlagSet{
					{
						Name:                    xxx2,
						APIKeys:                 []string{"other-api-key", "default-api-key"},
						PollingInterval:         50000,
						FileFormat:              yaml2,
						StartWithRetrieverError: true,
						Notifiers: []config.NotifierConf{
							{
								Kind:        config.WebhookNotifier,
								EndpointURL: httpLocalhost8080Webhook,
							},
						},
						Exporters: &[]config.ExporterConf{
							{
								Kind: kafka,
								Kafka: kafkaexporter.Settings{
									Addresses: []string{localhost19092, localhost19093},
								},
							},
						},
						Retrievers: &[]retrieverconf.RetrieverConf{
							{
								Kind: file2,
								Path: cmdRelayproxyTestdataDockerhub,
							},
						},
					},
				},
			},
			wantErr: assert.NoError,
			envVars: map[string]string{
				"FLAGSETS_0_NAME":                        xxx2,
				"FLAGSETS_0_APIKEYS":                     "other-api-key,default-api-key",
				"FLAGSETS_0_FILEFORMAT":                  yaml2,
				"FLAGSETS_0_POLLINGINTERVAL":             "50000",
				"FLAGSETS_0_STARTWITHRETRIEVERERROR":     "true",
				"FLAGSETS_0_NOTIFIERS_0_KIND":            webhook,
				"FLAGSETS_0_NOTIFIERS_0_ENDPOINTURL":     httpLocalhost8080Webhook,
				"FLAGSETS_0_EXPORTERS_0_KIND":            kafka,
				"FLAGSETS_0_EXPORTERS_0_KAFKA_ADDRESSES": localhost19092Localhost19093,
				"FLAGSETS_0_RETRIEVERS_0_KIND":           file2,
				"FLAGSETS_0_RETRIEVERS_0_PATH":           cmdRelayproxyTestdataDockerhub,
			},
		},
	})
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

			f := pflag.NewFlagSet(config3, pflag.ContinueOnError)
			f.String(config3, "", locationOfYourConfig)
			_ = f.Parse([]string{"--config=../testdata/config/valid-file.yaml"})

			got, err := config.New(f, slog.New(slog.DiscardHandler), wildcardVersion, withEnviron(environ...))
			require.NoError(t, err)
			assert.Equal(t, tt.wantEvaluation, got.AuthorizedKeys.Evaluation)
			assert.Equal(t, tt.wantAdmin, got.AuthorizedKeys.Admin)
		})
	}
}

func TestConfig_LogLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		config    *config.Config
		name      string
		wantDebug bool
	}{
		{
			name:      "no config",
			wantDebug: false,
		},
		{
			name: "invalid log level",
			config: &config.Config{
				LogLevel: invalid2,
			},
			wantDebug: false,
		},
		{
			name: "debug level",
			config: &config.Config{
				LogLevel: debug,
			},
			wantDebug: true,
		},
		{
			name: "info level",
			config: &config.Config{
				LogLevel: info2,
			},
			wantDebug: false,
		},
		{
			name: "error level",
			config: &config.Config{
				LogLevel: "error",
			},
			wantDebug: false,
		},
		{
			name: "panic level",
			config: &config.Config{
				LogLevel: "panic",
			},
			wantDebug: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equalf(t, tt.wantDebug, tt.config.IsDebugEnabled(), "IsDebugEnabled()")
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
				LogLevel: debug,
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

		f := pflag.NewFlagSet(config3, pflag.ContinueOnError)

		got, err := config.New(f, slog.New(slog.DiscardHandler), wildcardVersion, withEnviron(
			"RETRIEVERS_0_KIND="+string(retrieverconf.FileRetriever),
			"RETRIEVERS_0_PATH=cmd/relayproxy/testdata/dockerhub-example/flags.goff.yaml",
		))
		require.NoError(t, err)

		assert.Equal(t, retrieverconf.FileRetriever, (*got.Retrievers)[0].Kind)
		assert.Equal(t, cmdRelayproxyTestdataDockerhub, (*got.Retrievers)[0].Path)
	})

	t.Run("Flagset retriever", func(t *testing.T) {
		t.Parallel()

		f := pflag.NewFlagSet(config3, pflag.ContinueOnError)
		got, err := config.New(f, slog.New(slog.DiscardHandler), wildcardVersion, withEnviron(
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

	flagSet := pflag.NewFlagSet(config3, pflag.ContinueOnError)
	flagSet.String(config3, "", locationOfYourConfig)
	require.NoError(t, flagSet.Parse([]string{config2 + configPath}))
	got, err := config.New(flagSet, slog.New(slog.DiscardHandler), wildcardVersion, opts...)
	require.NoError(t, err)

	return got
}

func TestParseConfig_ExporterCleanQueueInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		content      string
		wantFlagSets []time.Duration
		want         time.Duration
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
		{name: unset, cfg: &config.Config{Retriever: retriever}},
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
		{name: "malformed entry in first position", attributes: token2 + marker + ",foo=bar", wantPosition: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			core, logs := slogutil.NewObserver(slog.LevelDebug)

			_, err := config.New(pflag.NewFlagSet(config3, pflag.ContinueOnError), core, wildcardVersion,
				withEnviron("OTEL_RESOURCE_ATTRIBUTES="+tt.attributes))
			require.NoError(t, err)

			entries := logs.FilterMessageSnippet("OTEL_RESOURCE_ATTRIBUTES").All()
			require.Len(t, entries, 1)
			assert.Equal(t, slog.LevelError, entries[0].Level)
			assert.Equal(t, tt.wantPosition, entries[0].ContextMap()["position"])

			for _, e := range logs.All() {
				assert.NotContains(t, fmt.Sprintf("%v %v", e.Message, e.ContextMap()), marker)
			}
		})
	}
}
