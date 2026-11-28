package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
)

// BenchmarkUserKeyAuthFlagSet measures the authentication of one request with the API key of a flag set, through
// the middleware of the evaluation endpoints in flag set mode. Allocations per operation must not increase.
func BenchmarkUserKeyAuthFlagSet(b *testing.B) {
	const apiKey = "tenant-key"

	manager := &mock.Resolver{Set: mock.Engine{GoFeatureFlag: &ffclient.GoFeatureFlag{}}}
	srv := &Server{keys: service.NewAPIKeysFrom(mock.Keys{Evaluation: []string{apiKey}}), resolver: manager}

	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	handler := srv.userKeyAuth().HTTP()(next)

	b.ReportAllocs()

	for b.Loop() {
		req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, "/v1/allflags", http.NoBody)
		req.Header.Set("X-Api-Key", apiKey)

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(b, http.StatusOK, rec.Code)
	}
}
