package helper_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func TestAPIKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		xAPIKey        string
		authorization  string
		expectedAPIKey string
	}{
		{name: "X-API-Key header takes precedence", xAPIKey: "x-api-key-value",
			authorization: "Bearer auth-header-value", expectedAPIKey: "x-api-key-value"},
		{name: "X-API-Key header only", xAPIKey: "x-api-key-only", expectedAPIKey: "x-api-key-only"},
		{name: "Bearer token", authorization: "Bearer my-api-key-123", expectedAPIKey: "my-api-key-123"},
		{name: "Bearer token with leading/trailing spaces", authorization: "Bearer   my-api-key-with-spaces  ",
			expectedAPIKey: "my-api-key-with-spaces"},
		{name: "bearer prefix is case-insensitive", authorization: "bEaReR key", expectedAPIKey: fixtures.MetadataKey},
		{name: "raw Authorization value", authorization: "raw-key", expectedAPIKey: "raw-key"},
		{name: "nothing", expectedAPIKey: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			if tt.xAPIKey != "" {
				req.Header.Set(helper.XAPIKeyHeader, tt.xAPIKey)
			}

			if tt.authorization != "" {
				req.Header.Set(protocol.HeaderAuthorization, tt.authorization)
			}

			assert.Equal(t, tt.expectedAPIKey, helper.APIKey(req))
		})
	}
}

func TestFlagSet(t *testing.T) {
	t.Parallel()

	flagset := &ffclient.GoFeatureFlag{}

	tests := []struct {
		manager     *mock.MockFlagsetManager
		wantFlagSet service.FlagSet
		name        string
		wantMessage string
		wantCode    int
	}{
		{
			name:        "nil manager",
			wantCode:    http.StatusInternalServerError,
			wantMessage: "flagset manager is not initialized",
		},
		{name: "unknown key", manager: &mock.MockFlagsetManager{GetFlagSetsErr: errNotFound},
			wantCode: http.StatusBadRequest, wantMessage: "error while getting flagset: not found"},
		{
			name:        "known key",
			manager:     &mock.MockFlagsetManager{FlagSets: map[string]*ffclient.GoFeatureFlag{"t": flagset}},
			wantFlagSet: flagset,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				got service.FlagSet
				err *helper.HTTPError
			)
			if tt.manager == nil {
				got, err = helper.FlagSet(nil, fixtures.MetadataKey)
			} else {
				got, err = helper.FlagSet(tt.manager, fixtures.MetadataKey)
			}

			assert.Equal(t, tt.wantFlagSet, got)

			if tt.wantCode == 0 {
				require.Nil(t, err)

				return
			}

			require.NotNil(t, err)
			assert.Equal(t, tt.wantCode, err.Code)
			assert.Equal(t, tt.wantMessage, err.Message)
		})
	}
}
