package controller_test

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/notifier"
)

const (
	configurationFlagsYaml    = "/configuration_flags.yaml"
	flagset1ApiKey            = "flagset1-api-key"
	impossibleToCreateFlagset = "impossible to create flagset manager"
	invalidAPIKey             = "invalid-api-key"
	requestsEmptyJSON         = "requests/empty.json"
	requestsFilterFlagsJSON   = "requests/filter-flags.json"
	requestsInvalidJSONJSON   = "requests/invalid-json.json"
)

const mockConfigFlagsLocation = testdataDir + "/configuration/"

func TestFlagConfigurationAPICtrl_Handler_DefaultMode(t *testing.T) {
	t.Parallel()

	type want struct {
		bodyLocation string
		statusCode   int
	}

	test := []struct {
		name        string
		requestBody string
		want        want
	}{
		{
			name:        "Test with empty body",
			requestBody: mockConfigFlagsLocation + requestsEmptyJSON,
			want: want{
				statusCode:   http.StatusOK,
				bodyLocation: mockConfigFlagsLocation + "responses/empty.json",
			},
		},
		{
			name:        "Test with empty flags ",
			requestBody: mockConfigFlagsLocation + "requests/empty-flag-array.json",
			want: want{
				statusCode:   http.StatusOK,
				bodyLocation: mockConfigFlagsLocation + "responses/empty-flag-array.json",
			},
		},
		{
			name:        "Filter flags",
			requestBody: mockConfigFlagsLocation + requestsFilterFlagsJSON,
			want: want{
				statusCode:   http.StatusOK,
				bodyLocation: mockConfigFlagsLocation + "responses/filter-flags.json",
			},
		},
		{
			name:        "Invalid JSON",
			requestBody: mockConfigFlagsLocation + requestsInvalidJSONJSON,
			want: want{
				statusCode: http.StatusBadRequest,
			},
		},
	}

	for _, tt := range test {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create config for default mode (no flagsets)
			conf := config.Config{
				Retriever: &retrieverconf.RetrieverConf{
					Kind: retrieverconf.FileRetriever,
					Path: testdataDir + configurationFlagsYaml,
				},
				Exporter: &config.ExporterConf{
					Kind: config.LogExporter,
				},
			}

			flagsetManager, err := service.NewFlagsetManager(
				&conf,
				slog.New(slog.DiscardHandler),
				[]notifier.Notifier{},
				nil,
				nil,
			)
			require.NoError(t, err, impossibleToCreateFlagset)

			ctrl := controller.NewAPIFlagConfiguration(flagsetManager, metric.Metrics{})

			// read the request body from the file
			requestBody, err := os.ReadFile(tt.requestBody)
			require.NoError(t, err)

			// Call the handler
			out, handlerErr := ctrl.Handle(t.Context(), &controller.FlagConfigurationInput{
				Request: controller.NewRequest("", protocol.MIMEApplicationJSON),
				RawBody: requestBody,
			})
			require.NoError(t, handlerErr)

			assert.Equal(t, tt.want.statusCode, out.Status)

			if tt.want.bodyLocation != "" {
				wantBody, readFileErr := os.ReadFile(tt.want.bodyLocation)
				require.NoError(t, readFileErr)
				assert.JSONEq(t, string(wantBody), jsonOf(t, out.Body))
			}
		})
	}
}

func TestFlagConfigurationAPICtrl_Handler_FlagsetMode(t *testing.T) {
	t.Parallel()

	const (
		configFlagsLocation  = testdataDir + configurationFlagsYaml
		configFlagsLocation2 = testdataDir + "/config_flags_v2.yaml"
	)

	type want struct {
		bodyLocation string
		statusCode   int
	}

	test := []struct {
		name        string
		requestBody string
		apiKey      string
		want        want
	}{
		{
			name:        "Test with empty body for flagset1",
			requestBody: mockConfigFlagsLocation + requestsEmptyJSON,
			apiKey:      flagset1ApiKey,
			want: want{
				statusCode:   http.StatusOK,
				bodyLocation: mockConfigFlagsLocation + "responses/empty.json",
			},
		},
		{
			name:        "Test with empty body for flagset2",
			requestBody: mockConfigFlagsLocation + requestsEmptyJSON,
			apiKey:      "flagset2-api-key",
			want: want{
				statusCode:   http.StatusOK,
				bodyLocation: mockConfigFlagsLocation + "responses/empty2.json",
			},
		},
		{
			name:        "Filter flags for flagset1",
			requestBody: mockConfigFlagsLocation + requestsFilterFlagsJSON,
			apiKey:      flagset1ApiKey,
			want: want{
				statusCode:   http.StatusOK,
				bodyLocation: mockConfigFlagsLocation + "responses/filter-flags.json",
			},
		},
		{
			name:        "Invalid JSON",
			requestBody: mockConfigFlagsLocation + requestsInvalidJSONJSON,
			apiKey:      flagset1ApiKey,
			want: want{
				statusCode: http.StatusBadRequest,
			},
		},
		{
			name:        "API key not linked to a flagset",
			requestBody: mockConfigFlagsLocation + requestsEmptyJSON,
			apiKey:      invalidAPIKey,
			want: want{
				statusCode: http.StatusBadRequest,
			},
		},
	}

	for _, tt := range test {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create config for flagset mode
			conf := config.Config{
				FlagSets: []config.FlagSet{
					{
						Name: "flagset1",
						Retriever: &retrieverconf.RetrieverConf{
							Kind: retrieverconf.FileRetriever,
							Path: configFlagsLocation,
						},
						Exporter: &config.ExporterConf{
							Kind: config.LogExporter,
						},
						APIKeys: []string{flagset1ApiKey},
					},
					{
						Name: "flagset2",
						Retriever: &retrieverconf.RetrieverConf{
							Kind: retrieverconf.FileRetriever,
							Path: configFlagsLocation2,
						},
						Exporter: &config.ExporterConf{
							Kind: config.LogExporter,
						},
						APIKeys: []string{"flagset2-api-key"},
					},
				},
			}

			flagsetManager, err := service.NewFlagsetManager(
				&conf,
				slog.New(slog.DiscardHandler),
				[]notifier.Notifier{},
				nil,
				nil,
			)
			require.NoError(t, err, impossibleToCreateFlagset)

			ctrl := controller.NewAPIFlagConfiguration(flagsetManager, metric.Metrics{})

			// read the request body from the file
			requestBody, err := os.ReadFile(tt.requestBody)
			require.NoError(t, err)

			// Call the handler, with the API key if provided
			out, err := ctrl.Handle(t.Context(), &controller.FlagConfigurationInput{
				Request: controller.NewRequest(tt.apiKey, protocol.MIMEApplicationJSON),
				RawBody: requestBody,
			})

			if tt.want.statusCode == http.StatusBadRequest && tt.apiKey == invalidAPIKey {
				require.Error(t, err, "handler should return an error for invalid API key")

				he := &helper.HTTPError{}

				ok := errors.As(err, &he)
				if ok {
					assert.Equal(t, tt.want.statusCode, he.Code)
				}

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want.statusCode, out.Status)

			if tt.want.bodyLocation != "" {
				wantBody, readFileErr := os.ReadFile(tt.want.bodyLocation)
				require.NoError(t, readFileErr)
				assert.JSONEq(t, string(wantBody), jsonOf(t, out.Body))
			}
		})
	}
}

func TestFlagConfigurationAPICtrl_Handler_FlagConfigurationMetric(t *testing.T) {
	t.Parallel()

	const (
		metricName = "semaphore_flag_configuration_total"
		want       = "# HELP " + metricName + " Counter events for number of configuration api requests.\n" +
			"# TYPE " + metricName + " counter\n" +
			metricName + " 1\n"
	)

	conf := config.Config{
		FlagSets: []config.FlagSet{
			{
				Name: "flagset1",
				Retriever: &retrieverconf.RetrieverConf{
					Kind: retrieverconf.FileRetriever,
					Path: testdataDir + configurationFlagsYaml,
				},
				APIKeys: []string{flagset1ApiKey},
			},
		},
	}
	flagsetManager, err := service.NewFlagsetManager(
		&conf,
		slog.New(slog.DiscardHandler),
		[]notifier.Notifier{},
		nil,
		nil,
	)
	require.NoError(t, err, impossibleToCreateFlagset)
	t.Cleanup(func() { flagsetManager.Close() })

	tests := []struct {
		name           string
		flagsetManager service.FlagsetManager
		requestBody    string
		apiKey         string
		wantStatusCode int
	}{
		{
			name:           "successful request",
			flagsetManager: flagsetManager,
			requestBody:    mockConfigFlagsLocation + requestsFilterFlagsJSON,
			apiKey:         flagset1ApiKey,
			wantStatusCode: http.StatusOK,
		},
		{
			name:           "invalid request body",
			flagsetManager: flagsetManager,
			requestBody:    mockConfigFlagsLocation + requestsInvalidJSONJSON,
			apiKey:         flagset1ApiKey,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:           "API key not linked to a flagset",
			flagsetManager: flagsetManager,
			requestBody:    mockConfigFlagsLocation + requestsEmptyJSON,
			apiKey:         invalidAPIKey,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:           "flagset manager not initialized",
			flagsetManager: nil,
			requestBody:    mockConfigFlagsLocation + requestsEmptyJSON,
			wantStatusCode: http.StatusInternalServerError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			metrics, metricsErr := metric.NewMetrics()
			require.NoError(t, metricsErr)

			requestBody, metricsErr := os.ReadFile(tt.requestBody)
			require.NoError(t, metricsErr)

			ctrl := controller.NewAPIFlagConfiguration(tt.flagsetManager, metrics)
			out, handlerErr := ctrl.Handle(t.Context(), &controller.FlagConfigurationInput{
				Request: controller.NewRequest(tt.apiKey, protocol.MIMEApplicationJSON),
				RawBody: requestBody,
			})

			var statusCode int
			if handlerErr == nil {
				statusCode = out.Status
			} else {
				var he *helper.HTTPError
				require.ErrorAs(t, handlerErr, &he)
				statusCode = he.Code
			}

			assert.Equal(t, tt.wantStatusCode, statusCode)
			assert.NoError(t, testutil.GatherAndCompare(metrics.Registry, strings.NewReader(want), metricName))
		})
	}
}
