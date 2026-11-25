package middleware

import (
	"net/http"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
)

const (
	// VersionHeaderName is the response header carrying the version of the relay proxy.
	VersionHeaderName = "X-Semaphore-Version"
	// LegacyVersionHeaderName is the name GO Feature Flag used for the same header, still sent
	// next to VersionHeaderName for the clients that read it.
	LegacyVersionHeaderName = "X-Gofeatureflag-Version"
)

// VersionHeader is a middleware that adds the version of the relay proxy in the headers
// VersionHeaderName and LegacyVersionHeaderName, unless the configuration disables them.
func VersionHeader(cfg *config.Config) Func {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cfg.DisableVersionHeader {
				h := w.Header()
				h.Set(VersionHeaderName, cfg.Version)
				h.Set(LegacyVersionHeaderName, cfg.Version)
			}

			next.ServeHTTP(w, r)
		})
	}
}
