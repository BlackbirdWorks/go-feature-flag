package middleware

import (
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

const (
	// VersionHeaderName is the response header carrying the version of the relay proxy.
	VersionHeaderName = "X-SEMAPHORE-VERSION"
	// LegacyVersionHeaderName is the name GO Feature Flag used for the same header, still sent
	// next to VersionHeaderName for the clients that read it.
	LegacyVersionHeaderName = "X-GOFEATUREFLAG-VERSION"
)

// VersionHeaderConfig defines the configuration for the middleware.
type VersionHeaderConfig struct {
	Skipper          middleware.Skipper
	RelayProxyConfig *config.Config
}

// VersionHeader is a middleware that adds the version of the relay proxy in the headers
// VersionHeaderName and LegacyVersionHeaderName.
func VersionHeader(cfg VersionHeaderConfig) echo.MiddlewareFunc {
	// Use provided skipper or fallback to the default skipper.
	skipper := cfg.Skipper
	if skipper == nil {
		skipper = DefaultVersionHeaderSkipper
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if skipper(c) {
				return next(c)
			}

			c.Response().Header().Set(VersionHeaderName, cfg.RelayProxyConfig.Version)
			c.Response().Header().Set(LegacyVersionHeaderName, cfg.RelayProxyConfig.Version)
			return next(c)
		}
	}
}

func DefaultVersionHeaderSkipper(_ *echo.Context) bool {
	return false
}
