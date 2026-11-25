package retriever_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/testhelpers/mock/mockretriever"
)

func TestMixLegacyTypesOfRetrievers(t *testing.T) {
	t.Parallel()

	simple := mockretriever.NewSimpleRetriever(simpleName)
	initRetrieverLegacy := mockretriever.NewInitializableRetrieverLegacy(fixtures.Legacy)
	initRetriever := mockretriever.NewInitializableRetriever(standardName)
	intRetrieverFlagset := mockretriever.NewInitializableRetrieverWithFlagset(fixtures.FlagsetNone)
	goff, err := ffclient.New(&ffclient.Config{
		PollingInterval: 10 * time.Second,
		Retrievers: []retriever.Retriever{
			simple,
			initRetrieverLegacy,
			initRetriever,
			intRetrieverFlagset,
		},
	})
	require.NoError(t, err)
	goff.Close()

	assert.True(t, simple.RetrieveCalled)

	assert.True(t, initRetrieverLegacy.InitCalled)
	assert.True(t, initRetrieverLegacy.StatusCalled)
	assert.True(t, initRetrieverLegacy.RetrieveCalled)
	assert.True(t, initRetrieverLegacy.ShutdownCalled)

	assert.True(t, initRetriever.InitCalled)
	assert.True(t, initRetriever.StatusCalled)
	assert.True(t, initRetriever.RetrieveCalled)
	assert.True(t, initRetriever.ShutdownCalled)

	assert.True(t, intRetrieverFlagset.InitCalled)
	assert.True(t, intRetrieverFlagset.StatusCalled)
	assert.True(t, intRetrieverFlagset.RetrieveCalled)
	assert.True(t, intRetrieverFlagset.ShutdownCalled)
}
