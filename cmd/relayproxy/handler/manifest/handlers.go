package manifest

import (
	"context"
	"log/slog"
	"net/http"
	"sort"

	"github.com/labstack/echo/v5"
	"go.opentelemetry.io/otel"

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
	flagsetManager service.FlagsetManager, metrics metric.Metrics, logger *slog.Logger) Ctrl {
	return Ctrl{
		flagsetManager: flagsetManager,
		metrics:        metrics,
		logger:         logger,
	}
}

type Ctrl struct {
	flagsetManager service.FlagsetManager
	logger         *slog.Logger
	metrics        metric.Metrics
}

// GetManifest is a GET endpoint to return the flags configured in Semaphore as a OpenFeature flag manifest.
// @Summary   GetManifest is a GET endpoint to return the flags configured in Semaphore.
// @Tags OpenFeature Manisfest API
// @Description **GET** request to the URL `/openfeature/v0/manifest` returns the project manifest
// @Description containing active flags. Archived flags are excluded.
// @Security     ApiKeyAuth
// @Produce      json
// @Accept	 	 json
// @Success      200  {object} model.ManifestSuccessResponse "Success"
// @Failure      401 {object}  model.ManifestError "Unauthorized"
// @Failure      403 {object}  model.ManifestError "Forbidden"
// @Failure      500 {object}  model.ManifestError "Internal server error"
// @Router       /openfeature/v0/manifest [GET]
func (m *Ctrl) GetManifest(c *echo.Context) error {
	tracer := otel.GetTracerProvider().Tracer(configfile.OtelTracerName)
	_, span := tracer.Start(c.Request().Context(), "getManifest")
	defer span.End()

	flagset, httpErr := helper.RequestFlagSet(c, m.flagsetManager)
	if httpErr != nil {
		return httpErr
	}

	flags, err := flagset.GetFlagsFromCacheWithContext(c.Request().Context())
	if err != nil {
		m.logger.LogAttrs(
			context.Background(),
			slog.LevelError,
			"error while getting flags from cache",
			cmdlog.Err(err),
		)

		return echo.NewHTTPError(http.StatusInternalServerError, "error while getting flags from cache")
	}

	manifest, err := manifestHelper.GenerateDefinitionFromFlags(flags, manifestHelper.WithLogger(m.logger))
	if err != nil {
		m.logger.LogAttrs(context.Background(), slog.LevelError, "error while generating manifest", cmdlog.Err(err))

		return echo.NewHTTPError(http.StatusInternalServerError, "error while generating manifest")
	}
	manifestSuccessResponse := model.ManifestSuccessResponse{
		Flags: func() []model.ManifestDefinitionSuccessResponse {
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

			return responses
		}(),
	}
	c.Response().Header().Set(manifestCapabilitiesHeader, manifestCapabilitiesValue)
	m.metrics.IncGetManifestCall()

	return c.JSON(http.StatusOK, manifestSuccessResponse)
}
