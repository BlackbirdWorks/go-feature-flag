package helper

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/blackbirdworks/huma-middleware/middleware/logging"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

// APIKey extracts the API key from the request headers.
// It checks headers in the following order of precedence:
// 1. X-API-Key header (raw value)
// 2. Authorization header (with "Bearer " prefix removed if present)
// Returns an empty string if no API key is found.
func APIKey(r *http.Request) string {
	return APIKeyFromHeaders(r.Header.Get)
}

// APIKeyFromHeaders is APIKey for a source that reads a header by its name.
func APIKeyFromHeaders(header func(string) string) string {
	// First, check X-API-Key header (takes precedence)
	if xAPIKey := header(XAPIKeyHeader); xAPIKey != "" {
		return xAPIKey
	}

	// Fall back to Authorization header
	apiKey := header(AuthorizationHeader)
	if len(apiKey) >= len(BearerPrefix) && strings.EqualFold(apiKey[:len(BearerPrefix)], BearerPrefix) {
		return strings.TrimSpace(apiKey[len(BearerPrefix):])
	}

	return apiKey
}

// FlagSet retrieves the flagset for the given API key from the flagset manager
// This layer ensure that the flagset manager is initialized and that the API key is valid.
func FlagSet(flagsetManager service.FlagsetManager, apiKey string) (*ffclient.GoFeatureFlag, *HTTPError) {
	if flagsetManager == nil {
		return nil, NewHTTPError(http.StatusInternalServerError, "flagset manager is not initialized")
	}

	flagset, err := flagsetManager.FlagSet(apiKey)
	if err != nil {
		return nil, NewHTTPError(http.StatusBadRequest, "error while getting flagset: "+err.Error())
	}

	return flagset, nil
}

// flagSetLogKey is the attribute of the logs of a request that carries the name of its flag set, once it is known.
const flagSetLogKey = "flagset"

// contextKey is the type of the keys of the values the authentication keeps in the context of a request.
type contextKey int

const (
	resolvedFlagSetKey contextKey = iota
	resolvedFlagSetNameKey
)

// AuthenticateFlagSet resolves the flag set of the API key of the request. It returns the context to give to the
// handler, which carries the flag set, and false if the key does not route to a flag set.
// The accepted keys and the routing of the flag sets are one table: authentication and handler use the flag set
// resolved here, so a reload that swaps the routing in between can not make an authenticated request fail.
func AuthenticateFlagSet(
	ctx context.Context, flagsetManager service.FlagsetManager, apiKey string,
) (context.Context, bool) {
	flagset, err := flagsetManager.FlagSet(apiKey)
	if err != nil {
		return ctx, false
	}

	return context.WithValue(ctx, resolvedFlagSetKey, flagset), true
}

// AuthenticateFlagSetName is AuthenticateFlagSet for the endpoints that only need the name of the flag set.
func AuthenticateFlagSetName(
	ctx context.Context, flagsetManager service.FlagsetManager, apiKey string,
) (context.Context, bool) {
	name, err := flagsetManager.FlagSetName(apiKey)
	if err != nil {
		return ctx, false
	}

	logging.AddAttrs(ctx, slog.String(flagSetLogKey, name))

	return context.WithValue(ctx, resolvedFlagSetNameKey, name), true
}

// RequestFlagSet returns the flag set of the request: the one resolved by the authentication when there is one,
// or else the one of the API key of the request (authentication disabled, default mode).
func RequestFlagSet(
	ctx context.Context, flagsetManager service.FlagsetManager, apiKey string,
) (*ffclient.GoFeatureFlag, *HTTPError) {
	if flagset, ok := ctx.Value(resolvedFlagSetKey).(*ffclient.GoFeatureFlag); ok {
		return flagset, nil
	}

	return FlagSet(flagsetManager, apiKey)
}

// RequestFlagSetName returns the name of the flag set of the request, like RequestFlagSet. The name is added to the
// logs of the request when it is looked up here: the authentication did it when it resolved the name.
func RequestFlagSetName(ctx context.Context, flagsetManager service.FlagsetManager, apiKey string) (string, error) {
	if name, ok := ctx.Value(resolvedFlagSetNameKey).(string); ok {
		return name, nil
	}

	name, err := flagsetManager.FlagSetName(apiKey)
	if err != nil {
		return name, fmt.Errorf("resolving the name of the flag set: %w", err)
	}

	logging.AddAttrs(ctx, slog.String(flagSetLogKey, name))

	return name, nil
}
