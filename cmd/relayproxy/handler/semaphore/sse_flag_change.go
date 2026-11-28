package semaphore

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/blackbirdworks/huma-middleware/middleware/logging"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
)

// SSEFlagChange is the controller for the SSE flag change endpoint.
type SSEFlagChange struct {
	sseService     stream.SSEService
	flagsetManager service.FlagSetResolver
}

// NewSSEFlagChange is the constructor to create a new controller to handle SSE
// requests to be notified about flag changes.
func NewSSEFlagChange(
	sseService stream.SSEService,
	flagsetManager service.FlagSetResolver,
) *SSEFlagChange {
	return &SSEFlagChange{
		sseService:     sseService,
		flagsetManager: flagsetManager,
	}
}

// Handler is the entry point for the SSE endpoint to be notified of flag changes.
func (h *SSEFlagChange) Handler(w http.ResponseWriter, r *http.Request) error {
	apiKey := r.URL.Query().Get("apiKey")

	flagsetName, err := helper.RequestFlagSetName(r.Context(), h.flagsetManager, apiKey)
	if err != nil {
		return fmt.Errorf(
			"binding the request of the flag change stream: %w",
			helper.NewHTTPError(http.StatusBadRequest, err.Error()),
		)
	}

	logging.FromContext(r.Context()).LogAttrs(
		r.Context(),
		slog.LevelDebug,
		"SSE client connecting",
	)

	// r3labs/sse routes by the "stream" query parameter.
	// We need to set the "stream" query parameter to the flagset name.
	// We also need to delete the "apiKey" query parameter.
	// This is because the r3labs/sse server will use the "stream" query parameter to route the
	// request to the correct flagset. After that point the apiKey is not needed anymore, so we delete it.
	query := r.URL.Query()
	if apiKey == "" {
		// no key: the stream of the clients without a key, closed when the public flags are withdrawn
		query.Set("stream", stream.AnonymousStreamPrefix+flagsetName)
	} else {
		query.Set("stream", flagsetName)
	}

	query.Del("apiKey")
	r.URL.RawQuery = query.Encode()

	if apiKey == "" {
		h.sseService.ServeHTTP(w, r)

		return nil
	}

	h.sseService.ServeKeyed(w, r, apiKey, func() bool {
		current, nameErr := h.flagsetManager.FlagSetName(apiKey)

		return nameErr == nil && current == flagsetName
	})

	return nil
}
