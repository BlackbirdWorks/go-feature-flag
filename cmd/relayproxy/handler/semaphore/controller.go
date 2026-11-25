package controller

import (
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/internal/protocol"
)

// RawHandler is the handler of an endpoint that is served without huma: it reads the request and writes the
// response itself, and returns the error to send when it did not write the response.
type RawHandler interface {
	Handler(w http.ResponseWriter, r *http.Request) error
}

// Request holds what the typed handlers read from the request besides their parameters: the API key and the
// content type of the body. huma fills it (it is not part of the description of the API).
type Request struct {
	apiKey      string
	contentType string
}

// NewRequest returns the Request of a request with the API key and the content type.
func NewRequest(apiKey, contentType string) Request {
	return Request{apiKey: apiKey, contentType: contentType}
}

// Resolve implements huma.Resolver.
func (r *Request) Resolve(ctx huma.Context) []error {
	r.apiKey = helper.APIKeyFromHeaders(ctx.Header)
	r.contentType = ctx.Header(protocol.HeaderContentType)

	return nil
}

// APIKey returns the API key of the request.
func (r *Request) APIKey() string { return r.apiKey }

// ContentType returns the content type of the body of the request.
func (r *Request) ContentType() string { return r.contentType }

// BodyOpts configures an operation that reads its own body: huma reads the bytes (the handler decodes them, so
// the error bodies stay the ones of the relay proxy). The body has no size limit, as before, but the read is bound
// by readTimeout (the read timeout of the server, which also bound it before): a slow or stalled upload ends.
//
// A readTimeout of zero or less means no bound: it is not the default (the server uses 30 seconds), an operator
// who disabled the read timeout of the server asked for it.
func BodyOpts(operation *huma.Operation, readTimeout time.Duration) {
	operation.MaxBodyBytes = -1

	operation.BodyReadTimeout = readTimeout
	if readTimeout <= 0 {
		operation.BodyReadTimeout = -1
	}

	operation.SkipValidateBody = true
	operation.SkipValidateParams = true
}

// compile-time check of the interface implemented by the type.
var _ huma.Resolver = (*Request)(nil)
