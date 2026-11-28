package semaphore

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// ForceFlagsRefresh is the controller of the endpoint that forces the refresh of the flags in the cache.
type ForceFlagsRefresh struct {
	flagsetManager service.FlagSetResolver
	metrics        *metric.Metrics
}

// RetrieverRefreshResponse is the response of the refresh endpoint.
type RetrieverRefreshResponse struct {
	Applied           *bool  `json:"applied,omitempty"`
	Result            string `json:"result,omitempty"`
	Reason            string `json:"reason,omitempty"`
	Rejection         string `json:"rejection,omitempty"`
	Generation        uint64 `json:"generation,omitempty"`
	AppliedGeneration uint64 `json:"appliedGeneration,omitempty"`
	Refreshed         bool   `json:"refreshed"`
	Changed           bool   `json:"changed,omitempty"`
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
func NewForceFlagsRefresh(flagsetManager service.FlagSetResolver, metrics *metric.Metrics) *ForceFlagsRefresh {
	return &ForceFlagsRefresh{flagsetManager: flagsetManager, metrics: metrics}
}

// Handle forces the refresh of the flags in the cache: it triggers the retrievers to get the latest version of the
// flags. It is used when you know explicitly that a flag has changed, and you want to trigger the collection of
// the new version.
func (h *ForceFlagsRefresh) Handle(ctx context.Context, input *RetrieverRefreshInput) (*RetrieverRefreshOutput, error) {
	h.metrics.IncForceRefresh()

	if refresher, ok := h.flagsetManager.(service.Refresher); ok {
		return h.refresh(ctx, refresher)
	}

	unsupported, ok := h.flagsetManager.(service.RefreshUnsupported)
	if ok {
		return nil, fmt.Errorf("refreshing on request: %w",
			helper.NewHTTPError(http.StatusNotImplemented, unsupported.RefreshUnsupported()))
	}

	flagset, httpErr := helper.RequestFlagSet(ctx, h.flagsetManager, input.APIKey())
	if httpErr != nil {
		return nil, fmt.Errorf("getting the flag set of the refresh: %w", httpErr)
	}

	_, span := otel.GetTracerProvider().Tracer(configfile.OtelTracerName).Start(ctx, "retrieverRefresh")
	defer span.End()

	forceRefreshStatus := flagset.ForceRefresh()
	span.SetAttributes(attribute.Bool("retrieverRefresh.refreshed", forceRefreshStatus))

	return &RetrieverRefreshOutput{
		Body: RetrieverRefreshResponse{
			Refreshed:  forceRefreshStatus,
			Result:     "",
			Reason:     "",
			Generation: 0,
			Changed:    false,
			Applied:    nil, AppliedGeneration: 0, Rejection: "",
		},
	}, nil
}

// refresh polls the control plane now. The answer says what the poll found; a poll that failed is an answer, not an
// error of the request.
func (*ForceFlagsRefresh) refresh(ctx context.Context, refresher service.Refresher) (*RetrieverRefreshOutput, error) {
	outcome, err := refresher.Refresh(ctx)
	applied := outcome.Applied

	switch {
	case errors.Is(err, service.ErrNotPolling):
		return nil, fmt.Errorf("refreshing on request: %w",
			helper.NewHTTPError(http.StatusServiceUnavailable, service.ErrNotPolling.Error()))
	case err != nil:
		return nil, fmt.Errorf("refreshing on request: %w",
			helper.NewHTTPError(http.StatusGatewayTimeout, "the request ended before the poll did"))
	}

	return &RetrieverRefreshOutput{Body: RetrieverRefreshResponse{
		Result: outcome.Result, Reason: outcome.Reason, Generation: outcome.Generation, Changed: outcome.Changed,
		Refreshed: outcome.Result == manifestreader.PollUpdated || outcome.Result == manifestreader.PollNotModified,
		Applied:   &applied, AppliedGeneration: outcome.AppliedGeneration, Rejection: outcome.Rejection,
	}}, nil
}
