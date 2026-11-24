package api

import (
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/manifest"
	"github.com/labstack/echo/v5"
)

func (s *Server) addManifestRoutes(cManifest manifest.ManifestCtrl, authMiddleware echo.MiddlewareFunc) {
	manifestGroup := s.apiEcho.Group("/openfeature/v0")
	manifestGroup.Use(authMiddleware)
	manifestGroup.GET("/manifest", cManifest.GetManifest)
}
