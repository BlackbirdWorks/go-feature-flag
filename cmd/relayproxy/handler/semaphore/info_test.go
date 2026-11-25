package controller_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/notifier"
)

var (
	errFlagsetManagerIsNot = errors.New("flagset manager is not initialized")
	errFailedToGetFlagsets = errors.New("failed to get flagsets")
)

func Test_info_Handler(t *testing.T) {
	t.Parallel()

	type want struct {
		httpCode   int
		handlerErr bool
	}

	tests := []struct {
		name   string
		config config.Config
		want   want
	}{
		{
			name: "valid info default mode",
			want: want{
				httpCode: http.StatusOK,
			},
			config: config.Config{
				CommonFlagSet: config.CommonFlagSet{
					Retriever: &retrieverconf.RetrieverConf{
						Kind: retrieverconf.FileRetriever,
						Path: testdataDir + "/config_flags.yaml",
					},
				},
			},
		},
		{
			name: "valid info flagset mode",
			want: want{
				httpCode: http.StatusOK,
			},
			config: config.Config{
				FlagSets: []config.FlagSet{
					{
						Name:    "teamA",
						APIKeys: []string{"teamA-api-key"},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: retrieverconf.FileRetriever,
							Path: testdataDir + "/config_flags.yaml",
						},
					},
					{
						Name:    "teamB",
						APIKeys: []string{"teamA-api-key"},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: retrieverconf.FileRetriever,
							Path: testdataDir + "/config_flags.yaml",
						},
					},
				},
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flagsetManager, err := service.NewFlagsetManager(
				&tt.config,
				slog.New(slog.DiscardHandler),
				[]notifier.Notifier{},
				nil,
				nil,
			)
			require.NoError(t, err, "impossible to create flagset manager")

			srv := service.NewMonitoring(flagsetManager)
			infoCtrl := controller.NewInfo(srv)

			out, res := infoCtrl.Handle(t.Context(), &struct{}{})

			if tt.want.handlerErr {
				assert.Error(t, res, "handler should return an error")

				return
			}

			require.NoError(t, res)
			assert.Equal(t, http.StatusOK, tt.want.httpCode)

			// Parse the response JSON to check the structure
			var response map[string]any
			encoded, err := json.Marshal(out.Body)
			require.NoError(t, err)
			err = json.Unmarshal(encoded, &response)
			require.NoError(t, err, "Response should be valid JSON")

			// Check that cacheRefresh field exists and is a valid timestamp
			cacheRefreshStr, exists := response["cacheRefresh"]
			assert.True(t, exists, "Response should contain cacheRefresh field")

			// Parse the timestamp string
			cacheRefreshRaw, ok := cacheRefreshStr.(string)
			require.True(t, ok, "cacheRefresh should be a string")
			cacheRefresh, err := time.Parse(time.RFC3339, cacheRefreshRaw)
			require.NoError(t, err, "cacheRefresh should be a valid RFC3339 timestamp")
			assert.False(t, cacheRefresh.IsZero(), "cacheRefresh should not be zero time")

			if tt.config.FlagSets != nil {
				flagsetsRaw, hasFlagsets := response["flagsets"]
				assert.True(t, hasFlagsets, "Response should contain flagsets field")
				flagsets, isMap := flagsetsRaw.(map[string]any)
				require.True(t, isMap, "flagsets should be a map")
				assert.Len(t, flagsets, len(tt.config.FlagSets), "Number of flagsets should match")
				for _, flagset := range tt.config.FlagSets {
					flagsetName := flagset.Name
					flagsetRefreshDateRaw, hasFlagset := flagsets[flagsetName]
					assert.True(t, hasFlagset, "Response should contain flagset %s field", flagsetName)

					flagsetRefreshDateStr, isStr := flagsetRefreshDateRaw.(string)
					require.True(t, isStr, "flagset refresh date should be a string")
					flagsetRefreshDate, parseErr := time.Parse(time.RFC3339, flagsetRefreshDateStr)
					require.NoError(t, parseErr, "flagsetRefreshDate should be a valid RFC3339 timestamp")
					assert.False(t, flagsetRefreshDate.IsZero(), "flagsetRefreshDate should not be zero time")
				}
			}
		})
	}
}

// Test_info_Handler_Error tests the error scenario in the info handler.
func Test_info_Handler_Error(t *testing.T) {
	t.Parallel()

	t.Run("monitoring service returns error", func(t *testing.T) {
		t.Parallel()

		// Create a mock monitoring service that returns an error
		mockMonitoring := &mock.MockMonitoringService{
			HealthResponse: model.HealthResponse{Initialized: true},
			InfoResponse:   model.InfoResponse{},
			InfoError:      errFlagsetManagerIsNot,
		}

		infoCtrl := controller.NewInfo(mockMonitoring)

		_, res := infoCtrl.Handle(t.Context(), &struct{}{})

		// Verify that the handler returns an error
		require.Error(t, res, "Handler should return an error when monitoring service fails")

		// Verify that the error is an HTTP error with status 500
		httpError := &helper.HTTPError{}
		ok := errors.As(res, &httpError)
		assert.True(t, ok, "Error should be an HTTP error")
		assert.Equal(t, http.StatusInternalServerError, httpError.Code, "Should return 500 status code")
		assert.Equal(t, "flagset manager is not initialized", httpError.Message, "Error message should match")
	})

	t.Run("monitoring service returns different error", func(t *testing.T) {
		t.Parallel()

		// Create a mock monitoring service that returns a different error
		mockMonitoring := &mock.MockMonitoringService{
			HealthResponse: model.HealthResponse{Initialized: true},
			InfoResponse:   model.InfoResponse{},
			InfoError:      errFailedToGetFlagsets,
		}

		infoCtrl := controller.NewInfo(mockMonitoring)

		_, res := infoCtrl.Handle(t.Context(), &struct{}{})

		// Verify that the handler returns an error
		require.Error(t, res, "Handler should return an error when monitoring service fails")

		// Verify that the error is an HTTP error with status 500
		httpError := &helper.HTTPError{}
		ok := errors.As(res, &httpError)
		assert.True(t, ok, "Error should be an HTTP error")
		assert.Equal(t, http.StatusInternalServerError, httpError.Code, "Should return 500 status code")
		assert.Equal(t, "failed to get flagsets", httpError.Message, "Error message should match")
	})
}
