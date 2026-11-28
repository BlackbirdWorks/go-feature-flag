package semaphore_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
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

// refresherResolver is a resolver that polls on request.
type refresherResolver struct {
	service.FlagSetResolver

	err     error
	outcome service.RefreshOutcome
}

func (r *refresherResolver) Refresh(context.Context) (service.RefreshOutcome, error) {
	return r.outcome, r.err
}

// The refresh answers what the poll found, and maps the failures of the request to a status.
func Test_retriever_refresh_Handler_polls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantBody controller.RetrieverRefreshResponse
		name     string
		wantErr  string
		resolver refresherResolver
	}{
		{
			name: "a new generation",
			resolver: refresherResolver{outcome: service.RefreshOutcome{
				Result: manifestreader.PollUpdated, Generation: 4, Changed: true, Applied: true, AppliedGeneration: 4,
			}},
			wantBody: controller.RetrieverRefreshResponse{
				Result: manifestreader.PollUpdated, Generation: 4, Changed: true, Refreshed: true, Reason: "",
				Applied: new(true), AppliedGeneration: 4,
			},
		},
		{
			name: "nothing new",
			resolver: refresherResolver{outcome: service.RefreshOutcome{
				Result: manifestreader.PollNotModified, Generation: 4, Applied: true, AppliedGeneration: 4,
			}},
			wantBody: controller.RetrieverRefreshResponse{
				Result: manifestreader.PollNotModified, Generation: 4, Refreshed: true, Reason: "", Changed: false,
				Applied: new(true), AppliedGeneration: 4,
			},
		},
		{
			name: "a poll that failed is an answer",
			resolver: refresherResolver{outcome: service.RefreshOutcome{
				Result: manifestreader.PollError, Reason: manifestreader.ReasonTimeout, Generation: 4, Applied: true,
				AppliedGeneration: 3,
			}},
			wantBody: controller.RetrieverRefreshResponse{
				Result: manifestreader.PollError, Reason: manifestreader.ReasonTimeout, Generation: 4,
				Refreshed: false, Changed: false, Applied: new(true), AppliedGeneration: 3,
			},
		},
		{
			name: "a generation the engine rejected",
			resolver: refresherResolver{outcome: service.RefreshOutcome{
				Result: manifestreader.PollUpdated, Generation: 5, Changed: true, Applied: false, AppliedGeneration: 5,
				Rejection: "build-failed",
			}},
			wantBody: controller.RetrieverRefreshResponse{
				Result: manifestreader.PollUpdated, Generation: 5, Changed: true, Refreshed: true, Reason: "",
				Applied: new(false), AppliedGeneration: 5, Rejection: "build-failed",
			},
		},
		{
			name:     "the relay does not poll",
			resolver: refresherResolver{err: fmt.Errorf("stopped: %w", service.ErrNotPolling)},
			wantErr:  "refreshing on request: code=503, message=the relay is not polling its control plane",
		},
		{
			name:     "the request ended first",
			resolver: refresherResolver{err: context.Canceled},
			wantErr:  "refreshing on request: code=504, message=the request ended before the poll did",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := controller.NewForceFlagsRefresh(&tt.resolver, &metric.Metrics{})

			out, err := ctrl.Handle(t.Context(), &controller.RetrieverRefreshInput{})
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantBody, out.Body)
		})
	}
}
