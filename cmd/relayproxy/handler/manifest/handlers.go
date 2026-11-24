package manifest

import (
	"context"
	"log/slog"
	"net/http"
	"sort"

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

func NewManifest(
	flagsetManager service.FlagsetManager, metrics metric.Metrics, logger *slog.Logger) *Ctrl {
	return &Ctrl{
		flagsetManager: flagsetManager,
		metrics:        metrics,
		logger:         logger,
	}
}

// Ctrl is the controller of the manifest endpoint.
type Ctrl struct {
	flagsetManager service.FlagsetManager
	logger         *slog.Logger
	metrics        metric.Metrics
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
		return nil, httpErr
	}

	flags, err := flagset.GetFlagsFromCacheWithContext(ctx)
	if err != nil {
		m.logger.LogAttrs(
			context.Background(),
			slog.LevelError,
			"error while getting flags from cache",
			cmdlog.Err(err),
		)

		return nil, helper.NewHTTPError(http.StatusInternalServerError, "error while getting flags from cache")
	}

	manifest, err := manifestHelper.GenerateDefinitionFromFlags(flags, manifestHelper.WithLogger(m.logger))
	if err != nil {
		m.logger.LogAttrs(context.Background(), slog.LevelError, "error while generating manifest", cmdlog.Err(err))

		return nil, helper.NewHTTPError(http.StatusInternalServerError, "error while generating manifest")
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
	sort.Slice(responses, func(i, j int) bool {
		return responses[i].Key < responses[j].Key
	})
	m.metrics.IncGetManifestCall()

	return &Output{
		Capabilities: manifestCapabilitiesValue,
		Body:         model.ManifestSuccessResponse{Flags: responses},
	}, nil
}
