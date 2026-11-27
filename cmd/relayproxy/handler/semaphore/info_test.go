package semaphore_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
)

var (
	errFlagsetManagerIsNot = errors.New("flagset manager is not initialized")
	errFailedToGetFlagsets = errors.New("failed to get flagsets")
)

func Test_info_Handler(t *testing.T) {
	t.Parallel()

	refreshed := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		info         model.InfoResponse
		name         string
		wantFlagsets []string
	}{
		{
			name: "valid info default mode",
			info: model.InfoResponse{LatestCacheRefresh: &refreshed},
		},
		{
			name: "valid info flagset mode",
			info: model.InfoResponse{
				LatestCacheRefresh: &refreshed,
				Flagsets:           map[string]time.Time{"teamA": refreshed, "teamB": refreshed},
			},
			wantFlagsets: []string{"teamA", "teamB"},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := &mock.MockMonitoringService{InfoResponse: tt.info}
			infoCtrl := controller.NewInfo(srv)

			out, res := infoCtrl.Handle(t.Context(), &struct{}{})
			require.NoError(t, res)

			var response map[string]any

			encoded, err := json.Marshal(out.Body)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(encoded, &response), "Response should be valid JSON")

			cacheRefresh, ok := response["cacheRefresh"].(string)
			require.True(t, ok, "cacheRefresh should be a string")

			parsed, err := time.Parse(time.RFC3339, cacheRefresh)
			require.NoError(t, err, "cacheRefresh should be a valid RFC3339 timestamp")
			assert.True(t, refreshed.Equal(parsed))

			flagsets, hasFlagsets := response["flagsets"].(map[string]any)
			if tt.wantFlagsets == nil {
				assert.False(t, hasFlagsets, "default mode has no flagsets")

				return
			}

			require.True(t, hasFlagsets, "flagsets should be a map")
			assert.Len(t, flagsets, len(tt.wantFlagsets))

			for _, name := range tt.wantFlagsets {
				date, isStr := flagsets[name].(string)
				require.True(t, isStr, "flagset %s refresh date should be a string", name)

				got, parseErr := time.Parse(time.RFC3339, date)
				require.NoError(t, parseErr)
				assert.True(t, refreshed.Equal(got))
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
