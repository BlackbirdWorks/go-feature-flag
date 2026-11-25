package postgresqlretriever_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rr "github.com/blackbirdworks/semaphore/retriever/postgresqlretriever"
)

const wantParseErr = "creating the PostgreSQL pool: cannot parse `not a postgres uri`: " +
	"failed to parse as keyword/value (invalid keyword/value)"

func TestPoolRegistry_GetPoolFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		uri     string
		wantErr string
	}{
		{
			name:    "invalid URI",
			uri:     "not a postgres uri",
			wantErr: wantParseErr,
		},
		{
			name: "unreachable database",
			uri:  "postgres://user:pw@127.0.0.1:1/db?connect_timeout=1&sslmode=disable",
			wantErr: "failed to connect to `user=user database=db`: 127.0.0.1:1 (127.0.0.1): dial error: dial tcp 127.0.0." +
				"1:1: connect: connection refused",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pools := rr.NewPoolRegistry()
			pool, err := pools.GetPool(t.Context(), tt.uri)

			require.ErrorContains(t, err, tt.wantErr)
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
		r       *rr.Retriever
		name    string
		wantErr string
		// wantShared is whether the retriever has the registry of the caller after Init, or a registry of its own
		wantShared bool
	}{
		{
			name: "with the registry of the caller", r: &rr.Retriever{URI: "not a postgres uri", Pools: pools},
			wantShared: true, wantErr: wantParseErr,
		},
		{
			name: "with its own registry", r: &rr.Retriever{URI: "not a postgres uri"},
			wantShared: false, wantErr: wantParseErr,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.r.Init(t.Context(), nil, nil)

			require.EqualError(t, err, tt.wantErr)
			require.NotNil(t, tt.r.Pools, "the retriever always has a registry after Init")
			assert.Equal(t, tt.wantShared, tt.r.Pools == pools)
		})
	}
}
