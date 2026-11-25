//go:build docker

package postgresqlretriever_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	rr "github.com/blackbirdworks/semaphore/retriever/postgresqlretriever"
)

func TestGetPool_MultipleURIsAndReuse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pools := rr.NewPoolRegistry()

	// Setup Container
	req := testcontainers.ContainerRequest{
		Image:        "postgres:15-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env:          map[string]string{"POSTGRES_PASSWORD": "password"},
		// The postgres entrypoint starts a temporary server to run init scripts, then restarts,
		// so the "ready" log line appears twice. Waiting for the first one lets us connect
		// during the restart and fail with "the database system is starting up".
		WaitingFor: wait.ForAll(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second),
		),
	}

	pgContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)

	defer pgContainer.Terminate(ctx)

	endpoint, err := pgContainer.Endpoint(ctx, "")
	require.NoError(t, err)

	baseURI := "postgres://postgres:password@" + endpoint + "/postgres?sslmode=disable"

	// Different URIs
	uri1 := baseURI + "&application_name=A"
	uri2 := baseURI + "&application_name=B"

	// First Calls (RefCount = 1)
	pool1a, err := pools.GetPool(ctx, uri1)
	require.NoError(t, err)
	assert.NotNil(t, pool1a)

	pool2a, err := pools.GetPool(ctx, uri2)
	require.NoError(t, err)
	assert.NotNil(t, pool2a)

	// Verify distinct pools
	assert.NotEqual(t, pool1a, pool2a)

	// Reuse Logic (RefCount = 2 for URI1)
	pool1b, err := pools.GetPool(ctx, uri1)
	require.NoError(t, err)
	assert.Equal(t, pool1a, pool1b, "Should return exact same pool instance")

	// Release Logic
	// URI1 RefCount: 2 -> 1
	pools.ReleasePool(ctx, uri1)

	// URI1 RefCount: 1 -> 0 (Closed & Removed)
	pools.ReleasePool(ctx, uri1)

	// Recreation Logic
	// URI1 should now create a NEW pool
	pool1c, err := pools.GetPool(ctx, uri1)
	require.NoError(t, err)
	assert.NotEqual(t, pool1a, pool1c, "Should be a new pool instance after full release")

	// Cleanup new pool
	pools.ReleasePool(ctx, uri1)

	// URI2 Cleanup verification
	pools.ReleasePool(ctx, uri2) // RefCount -> 0

	pool2b, err := pools.GetPool(ctx, uri2)
	require.NoError(t, err)
	assert.NotEqual(t, pool2a, pool2b, "URI2 should be recreated")

	pools.ReleasePool(ctx, uri2)
}
