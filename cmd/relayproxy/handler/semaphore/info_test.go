package controller_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
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

			e := echo.New()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/info", nil)
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			c := e.NewContext(req, rec)
			res := infoCtrl.Handler(c)

			if tt.want.handlerErr {
				assert.Error(t, res, "handler should return an error")

				return
			}

			assert.Equal(t, tt.want.httpCode, rec.Code, "Invalid HTTP Code")

			// Parse the response JSON to check the structure
			var response map[string]any
			err = json.Unmarshal(rec.Body.Bytes(), &response)
			require.NoError(t, err, "Response should be valid JSON")

			// Check that cacheRefresh field exists and is a valid timestamp
			cacheRefreshStr, exists := response["cacheRefresh"]
			assert.True(t, exists, "Response should contain cacheRefresh field")

			// Parse the timestamp string
			cacheRefresh, err := time.Parse(time.RFC3339, cacheRefreshStr.(string))
			require.NoError(t, err, "cacheRefresh should be a valid RFC3339 timestamp")
			assert.False(t, cacheRefresh.IsZero(), "cacheRefresh should not be zero time")

			if tt.config.FlagSets != nil {
				flagsets, hasFlagsets := response["flagsets"]
				assert.True(t, hasFlagsets, "Response should contain flagsets field")
				assert.Len(t, flagsets.(map[string]any), len(tt.config.FlagSets), "Number of flagsets should match")
				for _, flagset := range tt.config.FlagSets {
					flagsetName := flagset.Name
					flagsetRefreshDateStr, hasFlagset := flagsets.(map[string]any)[flagsetName]
					assert.True(t, hasFlagset, "Response should contain flagset %s field", flagsetName)

					flagsetRefreshDate, parseErr := time.Parse(time.RFC3339, flagsetRefreshDateStr.(string))
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

		e := echo.New()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/info", nil)
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		c := e.NewContext(req, rec)
		res := infoCtrl.Handler(c)

		// Verify that the handler returns an error
		require.Error(t, res, "Handler should return an error when monitoring service fails")

		// Verify that the error is an HTTP error with status 500
		httpError := &echo.HTTPError{}
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

		e := echo.New()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/info", nil)
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		c := e.NewContext(req, rec)
		res := infoCtrl.Handler(c)

		// Verify that the handler returns an error
		require.Error(t, res, "Handler should return an error when monitoring service fails")

		// Verify that the error is an HTTP error with status 500
		httpError := &echo.HTTPError{}
		ok := errors.As(res, &httpError)
		assert.True(t, ok, "Error should be an HTTP error")
		assert.Equal(t, http.StatusInternalServerError, httpError.Code, "Should return 500 status code")
		assert.Equal(t, "failed to get flagsets", httpError.Message, "Error message should match")
	})
}
