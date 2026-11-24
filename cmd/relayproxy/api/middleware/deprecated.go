package middleware

import "net/http"

// DeprecatedAlias adds the RFC 8594 Deprecation and Link headers pointing clients to the replacement of the
// endpoint. Sunset is not set until the removal date is decided.
func DeprecatedAlias(replacement string) Func {
	link := `<` + replacement + `>; rel="successor-version"`

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Deprecation", "true")
			w.Header().Set("Link", link)
			next.ServeHTTP(w, r)
		})
	}
}
