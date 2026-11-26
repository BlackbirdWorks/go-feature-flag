package middleware

import "net/http"

const (
	// VersionHeaderName is the response header carrying the version of the relay proxy.
	VersionHeaderName = "X-Semaphore-Version"
	// LegacyVersionHeaderName is the name GO Feature Flag used for the same header, still sent
	// next to VersionHeaderName for the clients that read it.
	LegacyVersionHeaderName = "X-Gofeatureflag-Version"
)

// VersionHeader is a middleware that adds the version of the relay proxy in the headers
// VersionHeaderName and LegacyVersionHeaderName, unless disabled is true.
func VersionHeader(version string, disabled bool) Func {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !disabled {
				h := w.Header()
				h.Set(VersionHeaderName, version)
				h.Set(LegacyVersionHeaderName, version)
			}

			next.ServeHTTP(w, r)
		})
	}
}
