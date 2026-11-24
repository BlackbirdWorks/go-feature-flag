package middleware

import (
	"github.com/labstack/echo/v5"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

// StreamAuthorizer is a middleware that checks the apiKey query param to authorize
// streaming connections (websocket / SSE).
// In flag set mode (flagsetManager is not nil) a key is accepted if it routes to a flag set, and the name of the
// flag set resolved here is the one the handler uses.
func StreamAuthorizer(config *config.Config, flagsetManager service.FlagsetManager) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			apiKey := c.QueryParam("apiKey")
			if config.IsAuthenticationEnabled() && !streamKeyAccepted(c, config, flagsetManager, apiKey) {
				return echo.ErrUnauthorized
			}

			return next(c)
		}
	}
}

func streamKeyAccepted(
	c *echo.Context, config *config.Config, flagsetManager service.FlagsetManager, apiKey string,
) bool {
	if flagsetManager == nil || flagsetManager.IsDefaultFlagSet() {
		return config.APIKeyExists(apiKey)
	}

	// An admin key is accepted and then refused by the handler, as documented.
	return helper.AuthenticateFlagSetName(c, flagsetManager, apiKey) || config.APIKeysAdminExists(apiKey)
}
