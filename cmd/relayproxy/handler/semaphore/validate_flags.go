package semaphore

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/dustin/go-humanize"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
	"github.com/blackbirdworks/semaphore/internal/protocol"
)

// ValidateFlags is the controller for the /admin/v1/flags/validate endpoint.
type ValidateFlags struct {
	metrics *metric.Metrics
	version string
	limits  config.ValidateFlagsEndpoint
}

// FlagValidationError is the validation error of a single flag.
type FlagValidationError struct {
	Flag    string `example:"my-flag"              json:"flag"`
	Message string `example:"missing default rule" json:"message"`
}

// FlagValidationResponse is the response of the validate flags endpoint.
type FlagValidationResponse struct {
	Version string                `example:"v1.0.0" json:"version"`
	Errors  []FlagValidationError `json:"errors"`
	Valid   bool                  `json:"valid"`
}

// NewValidateFlags initializes the controller for the /admin/v1/flags/validate endpoint.
// The unset (zero) limits are replaced by their default.
func NewValidateFlags(
	version string, metrics *metric.Metrics, limits config.ValidateFlagsEndpoint,
) *ValidateFlags {
	return &ValidateFlags{version: version, metrics: metrics, limits: limits.WithDefaults()}
}

// Handler validates a flag file without loading it in the relay proxy.
func (h *ValidateFlags) Handler(w http.ResponseWriter, r *http.Request) error {
	outcome, err := h.validate(w, r)
	h.metrics.IncValidateFlags(outcome)

	return err
}

// validate runs the validation and returns the outcome used for the metric alongside the handler error.
func (h *ValidateFlags) validate(w http.ResponseWriter, r *http.Request) (string, error) {
	const outcomeError = metric.ValidateFlagsOutcomeError

	format, maxSize, ok := h.formatFromContentType(r.Header.Get(protocol.HeaderContentType))
	if !ok {
		return outcomeError, fmt.Errorf("checking the content type of the flags to validate: %w", helper.NewHTTPError(
			http.StatusUnsupportedMediaType,
			"unsupported or missing Content-Type, use application/json, application/yaml or application/toml",
		))
	}

	if r.ContentLength > maxSize {
		return outcomeError, fmt.Errorf(
			"checking the size of the flags to validate: %w",
			helper.NewHTTPError(http.StatusRequestEntityTooLarge, tooLargeMessage(maxSize, format)),
		)
	}

	body := http.MaxBytesReader(w, r.Body, maxSize)

	data, err := io.ReadAll(body)
	if err != nil {
		if _, isMaxBytes := errors.AsType[*http.MaxBytesError](err); isMaxBytes {
			return outcomeError, fmt.Errorf(
				"reading the flags to validate: %w",
				helper.NewHTTPError(http.StatusRequestEntityTooLarge, tooLargeMessage(maxSize, format)),
			)
		}

		return outcomeError, fmt.Errorf(
			"reading the flags to validate: %w",
			helper.NewHTTPError(http.StatusBadRequest, "impossible to read the request body"),
		)
	}

	tracer := otel.GetTracerProvider().Tracer(configfile.OtelTracerName)

	_, span := tracer.Start(r.Context(), "validateFlags")
	defer span.End()

	flags, err := configfile.ParseConfig(data, format)
	if err != nil {
		return outcomeError, fmt.Errorf(
			"decoding the flags to validate: %w",
			helper.NewHTTPError(http.StatusBadRequest, err.Error()),
		)
	}

	resp := FlagValidationResponse{
		Version: h.version,
		Valid:   true,
		Errors:  make([]FlagValidationError, 0),
	}
	for _, flagErr := range configfile.ValidateFlags(flags) {
		resp.Errors = append(resp.Errors, FlagValidationError{
			Flag:    flagErr.Flag,
			Message: flagErr.Err.Error(),
		})
	}

	span.SetAttributes(
		attribute.Int("validateFlags.flagCount", len(flags)),
		attribute.Int("validateFlags.invalidCount", len(resp.Errors)),
	)

	status := http.StatusOK
	outcome := metric.ValidateFlagsOutcomeValid

	if len(resp.Errors) > 0 {
		resp.Valid = false
		status = http.StatusUnprocessableEntity
		outcome = metric.ValidateFlagsOutcomeInvalid
	}

	werr := helper.WriteJSON(w, status, resp)
	if werr != nil {
		return outcome, fmt.Errorf("writing the validation response: %w", werr)
	}

	return outcome, nil
}

// formatFromContentType returns the flag file format matching the content type and the maximum
// size of the body for this format.
// tooLargeMessage is the error message returned when the body exceeds the limit of its format.
func tooLargeMessage(maxSize int64, format string) string {
	// limits are validated as non-negative, the conversion cannot overflow.
	//nolint:gosec // G115: limits are validated as non-negative, so uint64(maxSize) cannot overflow
	size := humanize.IBytes(uint64(maxSize))

	return fmt.Sprintf("request body too large (max %s for %s)", size, format)
}

func (h *ValidateFlags) formatFromContentType(contentType string) (string, int64, bool) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", 0, false
	}

	switch mediaType {
	case "application/json":
		return "json", int64(h.limits.MaxJSONBodySize), true
	case "application/yaml", "application/x-yaml", "text/yaml":
		return "yaml", int64(h.limits.MaxYAMLBodySize), true
	case "application/toml":
		return "toml", int64(h.limits.MaxTOMLBodySize), true
	default:
		return "", 0, false
	}
}
