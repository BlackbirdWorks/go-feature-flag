package middleware

import (
	"net/http"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

// StreamAuthorizer is a middleware that checks the apiKey query param to authorize
// streaming connections (websocket / SSE).
// In flag set mode (flagsetManager is not nil) a key is accepted if it routes to a flag set, and the name of the
// flag set resolved here is the one the handler uses. exposeError adds the error to the body of a refusal.
func StreamAuthorizer(keys service.APIKeys, flagsetManager service.FlagsetManager, exposeError bool) Func {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := r.URL.Query().Get("apiKey")
			if keys.AuthenticationEnabled() {
				req, ok := streamKeyAccepted(r, keys, flagsetManager, apiKey)
				if !ok {
					helper.WriteError(w, r, helper.ErrUnauthorized, exposeError)

					return
				}

				r = req
			}

			next.ServeHTTP(w, r)
		})
	}
}

func streamKeyAccepted(
	r *http.Request, keys service.APIKeys, flagsetManager service.FlagsetManager, apiKey string,
) (*http.Request, bool) {
	if flagsetManager == nil || flagsetManager.IsDefaultFlagSet() {
		return r, keys.KeyExists(apiKey)
	}

	// An admin key is accepted and then refused by the handler, as documented.
	if ctx, ok := helper.AuthenticateFlagSetName(r.Context(), flagsetManager, apiKey); ok {
		return r.WithContext(ctx), true
	}

	return r, keys.AdminKeyExists(apiKey)
}
