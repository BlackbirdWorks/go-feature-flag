// Package middleware holds the HTTP middleware of the relay proxy, as plain [http.Handler] wrappers.
package middleware

import (
	"net/http"
	"slices"
)

// Func is a middleware: it wraps a handler in another one.
type Func = func(http.Handler) http.Handler

// Chain returns h wrapped in the middleware, the first one being the outermost.
func Chain(h http.Handler, middleware ...Func) http.Handler {
	for _, m := range slices.Backward(middleware) {
		h = m(h)
	}

	return h
}
