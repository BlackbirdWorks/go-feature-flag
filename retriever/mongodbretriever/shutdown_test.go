package mongodbretriever_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/mongodbretriever"
)

func TestMongoDBRetriever_ShutdownWithoutInit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		r          *mongodbretriever.Retriever
		wantStatus retriever.Status
	}{
		{
			name:       "a retriever that was never initialized",
			r:          &mongodbretriever.Retriever{},
			wantStatus: retriever.RetrieverNotReady,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.NoError(t, tt.r.Shutdown(t.Context()))
			assert.Equal(t, tt.wantStatus, tt.r.Status())
		})
	}
}
