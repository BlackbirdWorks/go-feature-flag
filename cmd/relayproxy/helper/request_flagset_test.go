package helper_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
)

var (
	errNoFlagSetAssociated = errors.New("no flag set associated to the API key")
	errNotFound            = errors.New("not found")
)

// namedManager is a flag set manager that routes the keys of names to a flag set name.
type namedManager struct {
	mock.MockFlagsetManager

	names map[string]string
}

func (m *namedManager) FlagSetName(apiKey string) (string, error) {
	if name, ok := m.names[apiKey]; ok {
		return name, nil
	}

	return "", errNoFlagSetAssociated
}

func newRequestContext(t *testing.T, apiKey string) *echo.Context {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(helper.XAPIKeyHeader, apiKey)

	return echo.New().NewContext(req, httptest.NewRecorder())
}

func TestRequestFlagSet(t *testing.T) {
	t.Parallel()

	routed := &ffclient.GoFeatureFlag{}
	manager := &mock.MockFlagsetManager{FlagSets: map[string]*ffclient.GoFeatureFlag{"tenant": routed}}
	unrouted := &mock.MockFlagsetManager{GetFlagSetsErr: errFlagsetNotFoundFor}

	tests := []struct {
		name         string
		authenticate bool
		manager      *mock.MockFlagsetManager
		wantFlagSet  *ffclient.GoFeatureFlag
		wantErr      bool
	}{
		{name: "the flag set resolved by the authentication is used", authenticate: true, manager: manager,
			wantFlagSet: routed},
		{name: "the flag set of the key is looked up without authentication", manager: manager,
			wantFlagSet: routed},
		{name: "a key without flag set is an error without authentication", manager: unrouted, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newRequestContext(t, "key")
			if tt.authenticate {
				require.True(t, helper.AuthenticateFlagSet(c, tt.manager, "key"))
				// the routing changed after the authentication: the resolved flag set is still the one used
				tt.manager = unrouted
			}

			flagset, err := helper.RequestFlagSet(c, tt.manager)
			assert.Equal(t, tt.wantFlagSet, flagset)
			assert.Equal(t, tt.wantErr, err != nil)
		})
	}
}

func TestAuthenticateFlagSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		manager *mock.MockFlagsetManager
		want    bool
	}{
		{name: "a key that routes to a flag set",
			manager: &mock.MockFlagsetManager{FlagSets: map[string]*ffclient.GoFeatureFlag{"t": {}}}, want: true},
		{name: "a key that does not route to a flag set",
			manager: &mock.MockFlagsetManager{GetFlagSetsErr: errNotFound}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, helper.AuthenticateFlagSet(newRequestContext(t, "key"), tt.manager, "key"))
		})
	}
}

func TestRequestFlagSetName(t *testing.T) {
	t.Parallel()

	manager := &namedManager{names: map[string]string{"key": "tenant"}}
	tests := []struct {
		name         string
		apiKey       string
		authenticate bool
		wantName     string
		wantAuth     bool
		wantErr      bool
	}{
		{name: "the name resolved by the authentication is used", apiKey: "key", authenticate: true,
			wantName: "tenant", wantAuth: true},
		{name: "the name of the key is looked up without authentication", apiKey: "key", wantName: "tenant"},
		{name: "a key without flag set is an error", apiKey: "other", wantErr: true},
		{name: "a key without flag set is not authenticated", apiKey: "other", authenticate: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newRequestContext(t, tt.apiKey)
			if tt.authenticate {
				assert.Equal(t, tt.wantAuth, helper.AuthenticateFlagSetName(c, manager, tt.apiKey))
			}

			name, err := helper.RequestFlagSetName(c, manager, tt.apiKey)
			assert.Equal(t, tt.wantName, name)
			assert.Equal(t, tt.wantErr, err != nil)
		})
	}
}
