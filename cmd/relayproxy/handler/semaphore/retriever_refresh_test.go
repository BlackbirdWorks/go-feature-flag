package semaphore_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
)

func Test_retriever_refresh_Handler_no_goff(t *testing.T) {
	t.Parallel()

	ctrl := controller.NewForceFlagsRefresh(nil, &metric.Metrics{})
	_, handlerErr := ctrl.Handle(t.Context(), &controller.RetrieverRefreshInput{})
	require.Error(t, handlerErr)
	assert.Equal(
		t,
		"getting the flag set of the refresh: code=500, message=flagset manager is not initialized",
		handlerErr.Error(),
	)
}

func Test_retriever_refresh_Handler_valid(t *testing.T) {
	t.Parallel()

	cfg := mock.FileConfig("../../../../testdata/flag-config.yaml")
	flagsetManager := mock.NewDefault(t, &cfg)

	// Get the default flagset to check refresh date
	defaultFlagset := flagsetManager.Client("default")
	previousRefresh := defaultFlagset.GetCacheRefreshDate()

	// the stepping clock of TestMain makes the forced refresh land on a strictly later instant
	ctrl := controller.NewForceFlagsRefresh(flagsetManager, &metric.Metrics{})
	_, handlerErr := ctrl.Handle(t.Context(), &controller.RetrieverRefreshInput{})
	require.NoError(t, handlerErr)

	assert.NotEqual(t, previousRefresh, defaultFlagset.GetCacheRefreshDate())
}
