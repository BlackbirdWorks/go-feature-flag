package postgresqlretriever_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rr "github.com/blackbirdworks/semaphore/retriever/postgresqlretriever"
)

func TestPoolRegistry_GetPoolFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		uri  string
	}{
		{name: "invalid URI", uri: "not a postgres uri"},
		{name: "unreachable database", uri: "postgres://user:pw@127.0.0.1:1/db?connect_timeout=1&sslmode=disable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pools := rr.NewPoolRegistry()
			pool, err := pools.GetPool(t.Context(), tt.uri)

			require.Error(t, err)
			assert.Nil(t, pool)
			// nothing was registered, so releasing is a no-op
			assert.NotPanics(t, func() { pools.ReleasePool(t.Context(), tt.uri) })
		})
	}
}

func TestRetriever_SharesTheRegistryOfTheCaller(t *testing.T) {
	t.Parallel()

	pools := rr.NewPoolRegistry()
	tests := []struct {
		name string
		r    *rr.Retriever
	}{
		{name: "with the registry of the caller", r: &rr.Retriever{URI: "not a postgres uri", Pools: pools}},
		{name: "with its own registry", r: &rr.Retriever{URI: "not a postgres uri"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.r.Init(t.Context(), nil, nil)

			require.Error(t, err)
			assert.NotNil(t, tt.r.Pools, "the retriever always has a registry after Init")
		})
	}
}
