package middleware

import (
	"net/http"
	"strings"
)

// CORS allows any origin and answers every preflight with a fixed list of methods and the headers the browser
// asked for. A request without an Origin header goes through untouched (but for Vary).
func CORS() Func {
	allowMethods := strings.Join([]string{
		http.MethodGet, http.MethodHead, http.MethodPut,
		http.MethodPatch, http.MethodPost, http.MethodDelete,
	}, ",")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hdr := w.Header()
			hdr.Add("Vary", "Origin")
			if r.Header.Get("Origin") == "" {
				next.ServeHTTP(w, r)

				return
			}
			hdr.Set("Access-Control-Allow-Origin", "*")
			if r.Method != http.MethodOptions || r.Header.Get("Access-Control-Request-Method") == "" {
				next.ServeHTTP(w, r)

				return
			}

			hdr.Add("Vary", "Access-Control-Request-Method")
			hdr.Add("Vary", "Access-Control-Request-Headers")
			hdr.Set("Access-Control-Allow-Methods", allowMethods)
			if requested := r.Header.Get("Access-Control-Request-Headers"); requested != "" {
				hdr.Set("Access-Control-Allow-Headers", requested)
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}
}
