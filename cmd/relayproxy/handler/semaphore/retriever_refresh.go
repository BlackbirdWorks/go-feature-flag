package semaphore

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
)

// ForceFlagsRefresh is the controller of the endpoint that forces the refresh of the flags in the cache.
type ForceFlagsRefresh struct {
	flagsetManager service.FlagsetManager
	metrics        *metric.Metrics
}

// RetrieverRefreshResponse is the response of the refresh endpoint.
type RetrieverRefreshResponse struct {
	Refreshed bool `json:"refreshed"`
}

// RetrieverRefreshInput is the request of the refresh endpoint.
type RetrieverRefreshInput struct {
	Request
}

// RetrieverRefreshOutput is the response of the refresh endpoint.
type RetrieverRefreshOutput struct {
	Body RetrieverRefreshResponse
}

// NewForceFlagsRefresh initialize the controller for the refresh endpoint.
func NewForceFlagsRefresh(flagsetManager service.FlagsetManager, metrics *metric.Metrics) *ForceFlagsRefresh {
	return &ForceFlagsRefresh{flagsetManager: flagsetManager, metrics: metrics}
}

// Handle forces the refresh of the flags in the cache: it triggers the retrievers to get the latest version of the
// flags. It is used when you know explicitly that a flag has changed, and you want to trigger the collection of
// the new version.
func (h *ForceFlagsRefresh) Handle(ctx context.Context, in *RetrieverRefreshInput) (*RetrieverRefreshOutput, error) {
	h.metrics.IncForceRefresh()

	flagset, httpErr := helper.RequestFlagSet(ctx, h.flagsetManager, in.APIKey())
	if httpErr != nil {
		return nil, fmt.Errorf("getting the flag set of the refresh: %w", httpErr)
	}

	_, span := otel.GetTracerProvider().Tracer(configfile.OtelTracerName).Start(ctx, "retrieverRefresh")
	defer span.End()

	forceRefreshStatus := flagset.ForceRefresh()
	span.SetAttributes(attribute.Bool("retrieverRefresh.refreshed", forceRefreshStatus))

	return &RetrieverRefreshOutput{Body: RetrieverRefreshResponse{Refreshed: forceRefreshStatus}}, nil
}
