package middleware

import (
	"bytes"
	"io"
	"net/http"
)

// maxDumpedRequestBytes is the part of a request body that is read ahead for the dump, the rest is not dumped.
const maxDumpedRequestBytes = 5 << 20

// BodyDump gives the body of every request that is not skipped to dump, once the request is handled. The body is
// read before the handler, which reads it from a copy.
func BodyDump(skipper func(r *http.Request) bool, dump func(r *http.Request, body []byte)) Func {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skipper(r) {
				next.ServeHTTP(w, r)

				return
			}

			var buf bytes.Buffer
			// a failed read is the handler's to report: it reads the same body.
			_, _ = io.Copy(&buf, io.LimitReader(r.Body, maxDumpedRequestBytes))
			body := buf.Bytes()
			orig := r.Body
			r.Body = struct {
				io.Reader
				io.Closer
			}{Reader: io.MultiReader(bytes.NewReader(body), orig), Closer: orig}

			next.ServeHTTP(w, r)
			dump(r, body)
		})
	}
}
