package manifest_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/manifest"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/notifier"
)

const (
	manifestTestdataDir        = "../../testdata/manifest"
	manifestConfigFlags        = manifestTestdataDir + "/config_flags.yaml"
	manifestConfigFlagsV2      = manifestTestdataDir + "/config_flags_v2.yaml"
	manifestSuccessResponse    = manifestTestdataDir + "/responses/valid_manifest.json"
	manifestSuccessResponseV2  = manifestTestdataDir + "/responses/valid_manifest_v2.json"
	manifestCapabilitiesHeader = "X-Manifest-Capabilities"
	manifestCapabilitiesValue  = "read"
	manifestEndpoint           = "/openfeature/v0/manifest"
)

func TestManifestCtrl_GetManifest_DefaultMode(t *testing.T) {
	t.Parallel()

	conf := &config.Config{
		PollingInterval: 10000,
		FileFormat:      "yaml",
		Retrievers: &[]retrieverconf.RetrieverConf{
			{
				Kind: retrieverconf.FileRetriever,
				Path: manifestConfigFlags,
			},
		},
	}

	flagsetManager, err := service.NewFlagsetManager(
		conf,
		slog.New(slog.DiscardHandler),
		[]notifier.Notifier{},
		nil,
		nil,
	)
	require.NoError(t, err, "failed to create flagset manager")
	defer flagsetManager.Close()

	ctrl := manifest.NewManifest(flagsetManager, metric.Metrics{}, slog.New(slog.DiscardHandler))
	out, err := ctrl.GetManifest(t.Context(), &manifest.Input{})
	require.NoError(t, err)
	assert.Equal(
		t,
		manifestCapabilitiesValue,
		out.Capabilities,
		"capabilities header missing or wrong",
	)
	body, err := json.Marshal(out.Body)
	require.NoError(t, err)
	assertManifestEqual(t, manifestSuccessResponse, body)
}

func TestManifestCtrl_GetManifest_FlagsetMode(t *testing.T) {
	t.Parallel()

	type want struct {
		bodyFile         string
		errorMsgContains string
		httpCode         int
		errorCode        int
		handlerErr       bool
	}

	tests := []struct {
		name   string
		apiKey string
		want   want
	}{
		{
			name:   "valid manifest for flagset1",
			apiKey: "flagset1-api-key",
			want: want{
				httpCode: http.StatusOK,
				bodyFile: manifestSuccessResponse,
			},
		},
		{
			name:   "valid manifest for flagset2",
			apiKey: "flagset2-api-key",
			want: want{
				httpCode: http.StatusOK,
				bodyFile: manifestSuccessResponseV2,
			},
		},
		{
			name:   "API key not linked to a flagset",
			apiKey: "invalid-api-key",
			want: want{
				handlerErr:       true,
				errorCode:        http.StatusBadRequest,
				errorMsgContains: "error while getting flagset",
			},
		},
		{
			name:   "missing API key",
			apiKey: "",
			want: want{
				handlerErr:       true,
				errorCode:        http.StatusBadRequest,
				errorMsgContains: "error while getting flagset",
			},
		},
	}

	conf := &config.Config{
		FlagSets: []config.FlagSet{
			{
				Name:            "flagset1",
				PollingInterval: 10000,
				FileFormat:      "yaml",
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: retrieverconf.FileRetriever,
						Path: manifestConfigFlags,
					},
				},
				APIKeys: []string{"flagset1-api-key"},
			},
			{
				Name:            "flagset2",
				PollingInterval: 10000,
				FileFormat:      "yaml",
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: retrieverconf.FileRetriever,
						Path: manifestConfigFlagsV2,
					},
				},
				APIKeys: []string{"flagset2-api-key"},
			},
		},
	}

	flagsetManager, err := service.NewFlagsetManager(
		conf,
		slog.New(slog.DiscardHandler),
		[]notifier.Notifier{},
		nil,
		nil,
	)
	require.NoError(t, err, "failed to create flagset manager")
	t.Cleanup(func() { flagsetManager.Close() })

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := manifest.NewManifest(flagsetManager, metric.Metrics{}, slog.New(slog.DiscardHandler))
			out, handlerErr := ctrl.GetManifest(t.Context(), &manifest.Input{
				Request: controller.NewRequest(tt.apiKey, ""),
			})

			if tt.want.handlerErr {
				require.Error(t, handlerErr, "handler should return an error")
				he := &helper.HTTPError{}
				ok := errors.As(handlerErr, &he)
				assert.True(t, ok, "handler error should be an helper.HTTPError")
				assert.Equal(t, tt.want.errorCode, he.Code)
				assert.Contains(t, he.Message, tt.want.errorMsgContains)

				return
			}

			require.NoError(t, handlerErr)
			assert.Equal(t, http.StatusOK, tt.want.httpCode, "Invalid HTTP Code")
			assert.Equal(
				t,
				manifestCapabilitiesValue,
				out.Capabilities,
				"capabilities header missing or wrong",
			)

			if tt.want.bodyFile != "" {
				body, marshalErr := json.Marshal(out.Body)
				require.NoError(t, marshalErr)
				assertManifestEqual(t, tt.want.bodyFile, body)
			}
		})
	}
}

func TestManifestCtrl_GetManifest_NilFlagsetManager(t *testing.T) {
	t.Parallel()

	ctrl := manifest.NewManifest(nil, metric.Metrics{}, slog.New(slog.DiscardHandler))
	_, handlerErr := ctrl.GetManifest(t.Context(), &manifest.Input{})

	require.Error(t, handlerErr, "handler should return an error when flagset manager is nil")
	he := &helper.HTTPError{}
	ok := errors.As(handlerErr, &he)
	assert.True(t, ok, "handler error should be an helper.HTTPError")
	assert.Equal(t, http.StatusInternalServerError, he.Code)
	assert.Equal(t, "flagset manager is not initialized", he.Message)
}

// assertManifestEqual deserializes the actual body and the expected file into
// ManifestSuccessResponse structs and compares them element-wise. This is needed
// because the manifest is built from a Go map, so the order of flags in the
// response is not deterministic.
func assertManifestEqual(t *testing.T, expectedFile string, actualBody []byte) {
	t.Helper()

	expectedRaw, err := os.ReadFile(expectedFile)
	assert.NoError(t, err, "Impossible to read expected body file %s", expectedFile)

	var expected model.ManifestSuccessResponse
	assert.NoError(t, json.Unmarshal(expectedRaw, &expected), "invalid expected manifest JSON")

	var actual model.ManifestSuccessResponse
	assert.NoError(t, json.Unmarshal(actualBody, &actual), "invalid actual manifest JSON")

	assert.ElementsMatch(t, expected.Flags, actual.Flags, "manifest flags mismatch")
}
