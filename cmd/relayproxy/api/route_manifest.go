package api

import (
	"github.com/labstack/echo/v5"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/manifest"
)

func (s *Server) addManifestRoutes(cManifest manifest.Ctrl, authMiddleware echo.MiddlewareFunc) {
	manifestGroup := s.apiEcho.Group("/openfeature/v0")
	manifestGroup.Use(authMiddleware)
	manifestGroup.GET("/manifest", cManifest.GetManifest)
}
