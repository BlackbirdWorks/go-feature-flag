package helper

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v5"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

// APIKey extracts the API key from the request headers.
// It checks headers in the following order of precedence:
// 1. X-API-Key header (raw value)
// 2. Authorization header (with "Bearer " prefix removed if present)
// Returns an empty string if no API key is found.
func APIKey(c *echo.Context) string {
	// First, check X-API-Key header (takes precedence)
	if xAPIKey := c.Request().Header.Get(XAPIKeyHeader); xAPIKey != "" {
		return xAPIKey
	}

	// Fall back to Authorization header
	apiKey := c.Request().Header.Get(AuthorizationHeader)
	if len(apiKey) >= len(BearerPrefix) && strings.EqualFold(apiKey[:len(BearerPrefix)], BearerPrefix) {
		return strings.TrimSpace(apiKey[len(BearerPrefix):])
	}

	return apiKey
}

// FlagSet retrieves the flagset for the given API key from the flagset manager
// This layer ensure that the flagset manager is initialized and that the API key is valid.
func FlagSet(flagsetManager service.FlagsetManager, apiKey string) (*ffclient.GoFeatureFlag, *echo.HTTPError) {
	if flagsetManager == nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, "flagset manager is not initialized")
	}
	flagset, err := flagsetManager.FlagSet(apiKey)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "error while getting flagset: "+err.Error())
	}

	return flagset, nil
}

// Keys of the values stored in the echo context by the authentication of a request.
const (
	resolvedFlagSetKey     = "semaphore.resolvedFlagSet"
	resolvedFlagSetNameKey = "semaphore.resolvedFlagSetName"
)

// AuthenticateFlagSet resolves the flag set of the API key of the request and keeps it in the context, it returns
// false if the key does not route to a flag set.
// The accepted keys and the routing of the flag sets are one table: authentication and handler use the flag set
// resolved here, so a reload that swaps the routing in between can not make an authenticated request fail.
func AuthenticateFlagSet(c *echo.Context, flagsetManager service.FlagsetManager, apiKey string) bool {
	flagset, err := flagsetManager.FlagSet(apiKey)
	if err != nil {
		return false
	}
	c.Set(resolvedFlagSetKey, flagset)

	return true
}

// AuthenticateFlagSetName is AuthenticateFlagSet for the endpoints that only need the name of the flag set.
func AuthenticateFlagSetName(c *echo.Context, flagsetManager service.FlagsetManager, apiKey string) bool {
	name, err := flagsetManager.FlagSetName(apiKey)
	if err != nil {
		return false
	}
	c.Set(resolvedFlagSetNameKey, name)

	return true
}

// RequestFlagSet returns the flag set of the request: the one resolved by the authentication when there is one,
// or else the one of the API key of the request (authentication disabled, default mode).
func RequestFlagSet(c *echo.Context, flagsetManager service.FlagsetManager) (*ffclient.GoFeatureFlag, *echo.HTTPError) {
	if flagset, ok := c.Get(resolvedFlagSetKey).(*ffclient.GoFeatureFlag); ok {
		return flagset, nil
	}

	return FlagSet(flagsetManager, APIKey(c))
}

// RequestFlagSetName returns the name of the flag set of the request, like RequestFlagSet.
func RequestFlagSetName(c *echo.Context, flagsetManager service.FlagsetManager, apiKey string) (string, error) {
	if name, ok := c.Get(resolvedFlagSetNameKey).(string); ok {
		return name, nil
	}

	return flagsetManager.FlagSetName(apiKey)
}

// FlagKey returns the flagKey path parameter, unescaped.
// When a request path contains escapes that Go would not produce itself (e.g. %3A for ":"),
// the router matches on the raw path and the parameter keeps its escaping.
func FlagKey(c *echo.Context) string {
	key := c.Param("flagKey")
	if c.Request().URL.RawPath == "" {
		return key
	}
	if decoded, err := url.PathUnescape(key); err == nil {
		return decoded
	}

	return key
}
