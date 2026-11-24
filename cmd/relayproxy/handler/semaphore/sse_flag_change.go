package controller

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
)

// NewSSEFlagChange is the constructor to create a new controller to handle SSE
// requests to be notified about flag changes.
func NewSSEFlagChange(
	sseService stream.SSEService,
	flagsetManager service.FlagsetManager,
	logger *slog.Logger,
) RawHandler {
	return &sseFlagChange{
		sseService:     sseService,
		flagsetManager: flagsetManager,
		logger:         logger,
	}
}

type sseFlagChange struct {
	sseService     stream.SSEService
	flagsetManager service.FlagsetManager
	logger         *slog.Logger
}

// Handler is the entry point for the SSE endpoint to be notified of flag changes.
func (h *sseFlagChange) Handler(w http.ResponseWriter, r *http.Request) error {
	apiKey := r.URL.Query().Get("apiKey")
	flagsetName, err := helper.RequestFlagSetName(r.Context(), h.flagsetManager, apiKey)
	if err != nil {
		return helper.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	h.logger.LogAttrs(
		context.Background(),
		slog.LevelDebug,
		"SSE client connecting",
		slog.String("flagset", flagsetName),
	)

	// r3labs/sse routes by the "stream" query parameter.
	// We need to set the "stream" query parameter to the flagset name.
	// We also need to delete the "apiKey" query parameter.
	// This is because the r3labs/sse server will use the "stream" query parameter to route the
	// request to the correct flagset. After that point the apiKey is not needed anymore, so we delete it.
	q := r.URL.Query()
	q.Set("stream", flagsetName)
	q.Del("apiKey")
	r.URL.RawQuery = q.Encode()

	h.sseService.ServeHTTP(w, r)

	return nil
}
