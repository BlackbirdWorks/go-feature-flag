package manifest

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"github.com/blackbirdworks/huma-middleware/middleware/logging"
	"go.opentelemetry.io/otel"

	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
	manifestHelper "github.com/blackbirdworks/semaphore/cmdhelpers/manifest"
)

const (
	manifestCapabilitiesHeader = "X-Manifest-Capabilities"
	manifestCapabilitiesValue  = "read"
)

// Ctrl is the controller of the manifest endpoint.
type Ctrl struct {
	flagsetManager service.FlagsetManager
	metrics        metric.Metrics
}

// NewManifest returns the handler that serves the flag manifest of a flagset.
func NewManifest(
	flagsetManager service.FlagsetManager, metrics metric.Metrics) *Ctrl {
	return &Ctrl{
		flagsetManager: flagsetManager,
		metrics:        metrics,
	}
}

// Input is the request of the manifest endpoint.
type Input struct {
	controller.Request
}

// Output is the response of the manifest endpoint.
type Output struct {
	Capabilities string `header:"X-Manifest-Capabilities"`
	Body         model.ManifestSuccessResponse
}

// GetManifest returns the flags configured in Semaphore as an OpenFeature flag manifest: the active flags,
// the archived flags are excluded.
func (m *Ctrl) GetManifest(ctx context.Context, in *Input) (*Output, error) {
	ctx, span := otel.GetTracerProvider().Tracer(configfile.OtelTracerName).Start(ctx, "getManifest")
	defer span.End()

	flagset, httpErr := helper.RequestFlagSet(ctx, m.flagsetManager, in.APIKey())
	if httpErr != nil {
		return nil, fmt.Errorf("getting the flag set of the manifest: %w", httpErr)
	}

	logger := logging.FromContext(ctx)

	flags, err := flagset.GetFlagsFromCacheWithContext(ctx)
	if err != nil {
		logger.LogAttrs(
			ctx,
			slog.LevelError,
			"error while getting flags from cache",
			cmdlog.Err(err),
		)

		return nil, fmt.Errorf(
			"reading the flags of the manifest: %w",
			helper.NewHTTPError(http.StatusInternalServerError, "error while getting flags from cache"),
		)
	}

	manifest, err := manifestHelper.GenerateDefinitionFromFlags(flags, manifestHelper.WithLogger(logger))
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "error while generating manifest", cmdlog.Err(err))

		return nil, fmt.Errorf(
			"generating the manifest: %w",
			helper.NewHTTPError(http.StatusInternalServerError, "error while generating manifest"),
		)
	}

	responses := make([]model.ManifestDefinitionSuccessResponse, 0, len(manifest))
	for k, v := range manifest {
		responses = append(responses, model.ManifestDefinitionSuccessResponse{
			Key:          k,
			FlagType:     v.FlagType,
			DefaultValue: v.DefaultValue,
			Description:  v.Description,
		})
	}

	slices.SortFunc(responses, func(a, b model.ManifestDefinitionSuccessResponse) int {
		return cmp.Compare(a.Key, b.Key)
	})
	m.metrics.IncGetManifestCall()

	return &Output{
		Capabilities: manifestCapabilitiesValue,
		Body:         model.ManifestSuccessResponse{Flags: responses},
	}, nil
}
