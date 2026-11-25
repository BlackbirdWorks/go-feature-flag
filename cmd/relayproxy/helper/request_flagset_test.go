package helper_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackbirdworks/huma-middleware/middleware/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/testhelpers/slogutil"
)

const (
	tenant = "tenant"
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

func TestRequestFlagSet(t *testing.T) {
	t.Parallel()

	routed := &ffclient.GoFeatureFlag{}
	manager := &mock.MockFlagsetManager{FlagSets: map[string]*ffclient.GoFeatureFlag{tenant: routed}}
	unrouted := &mock.MockFlagsetManager{GetFlagSetsErr: errNotFound}

	tests := []struct {
		manager      *mock.MockFlagsetManager
		wantFlagSet  *ffclient.GoFeatureFlag
		name         string
		authenticate bool
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

			ctx := t.Context()
			if tt.authenticate {
				authCtx, ok := helper.AuthenticateFlagSet(ctx, tt.manager, fixtures.MetadataKey)
				require.True(t, ok)

				ctx = authCtx
				// the routing changed after the authentication: the resolved flag set is still the one used
				tt.manager = unrouted
			}

			flagset, err := helper.RequestFlagSet(ctx, tt.manager, fixtures.MetadataKey)
			assert.Equal(t, tt.wantFlagSet, flagset)
			assert.Equal(t, tt.wantErr, err != nil)
		})
	}
}

func TestAuthenticateFlagSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		manager *mock.MockFlagsetManager
		name    string
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

			_, ok := helper.AuthenticateFlagSet(t.Context(), tt.manager, fixtures.MetadataKey)
			assert.Equal(t, tt.want, ok)
		})
	}
}

func TestRequestFlagSetName(t *testing.T) {
	t.Parallel()

	manager := &namedManager{names: map[string]string{fixtures.MetadataKey: tenant}}

	tests := []struct {
		name         string
		apiKey       string
		wantName     string
		wantLogged   []any
		authenticate bool
		wantAuth     bool
		wantErr      bool
	}{
		{name: "the name resolved by the authentication is used", apiKey: fixtures.MetadataKey, authenticate: true,
			wantName: tenant, wantAuth: true, wantLogged: []any{tenant}},
		{
			name:       "the name of the key is looked up without authentication",
			apiKey:     fixtures.MetadataKey,
			wantName:   tenant,
			wantLogged: []any{tenant},
		},
		{name: "a key without flag set is an error", apiKey: "other", wantErr: true},
		{name: "a key without flag set is not authenticated", apiKey: "other", authenticate: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, logs := slogutil.NewObserver(slog.LevelDebug)

			var served *http.Request

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			logging.Handler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { served = r }),
				logging.WithLogger(logger)).ServeHTTP(httptest.NewRecorder(), req)

			ctx := served.Context()
			if tt.authenticate {
				authCtx, ok := helper.AuthenticateFlagSetName(ctx, manager, tt.apiKey)
				assert.Equal(t, tt.wantAuth, ok)

				ctx = authCtx
			}

			name, err := helper.RequestFlagSetName(ctx, manager, tt.apiKey)
			assert.Equal(t, tt.wantName, name)
			assert.Equal(t, tt.wantErr, err != nil)

			// the name is added to the logs of the request, once, however it was resolved.
			logging.FromContext(ctx).InfoContext(ctx, "probe")
			require.Equal(t, 1, logs.Len())

			var logged []any

			for _, attr := range logs.All()[0].Attrs {
				if attr.Key == "flagset" {
					logged = append(logged, attr.Value.Any())
				}
			}

			assert.Equal(t, tt.wantLogged, logged)
		})
	}
}
