package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	reqlog "github.com/blackbirdworks/semaphore/cmdhelpers/api/middleware"
	"github.com/blackbirdworks/semaphore/internal/protocol"
)

var (
	errHeaderMissing = errors.New("missing value in request header")
	errHeaderInvalid = errors.New("invalid value in request header")
	errKeyInvalid    = helper.NewHTTPError(http.StatusUnauthorized, "invalid key")
)

// KeyAuthConfig defines the configuration of the API key authentication.
type KeyAuthConfig struct {
	// Validator reports whether the key is accepted. It returns the context to give to the handler, which may
	// carry values resolved while validating the key.
	Validator func(ctx context.Context, key string) (context.Context, bool)
	// Skipper reports that the requests need no authentication.
	Skipper func() bool
	// AllowAnonymous makes the Validator decide a request that carries no key at all: it is called with an empty
	// key. A request with a key, right or wrong, is never given to it that way.
	AllowAnonymous bool
}

// KeyAuth is the middleware that authenticates a request with the API key of its X-API-Key header, or else with
// the bearer token of its Authorization header. A request that is refused gets a 401 and a {"message": ...} body.
// It has two entry points that share the same rules: [KeyAuth.HTTP] for a [http.Handler], [KeyAuth.Huma] for a
// huma operation.
type KeyAuth struct {
	cfg KeyAuthConfig
}

// NewKeyAuth returns the authentication middleware of the configuration.
func NewKeyAuth(cfg KeyAuthConfig) KeyAuth { return KeyAuth{cfg: cfg} }

// AllowsAnonymous reports whether a request without a key reaches the validator: the operations behind this
// middleware are documented with an optional key.
func (k KeyAuth) AllowsAnonymous() bool { return k.cfg.AllowAnonymous }

// HTTP is the entry point for a [http.Handler].
func (k KeyAuth) HTTP() Func {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, err := k.authenticate(
				r.Context(),
				r.Header.Get,
				func(name string) []string { return r.Header.Values(name) },
			)
			if err != nil {
				helper.WriteError(w, r, helper.NewHTTPError(http.StatusUnauthorized, err.Error()), false)

				return
			}

			if ctx != r.Context() {
				r = r.WithContext(ctx)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Huma is the entry point for a huma operation, it only uses the huma.Context interface.
func (k KeyAuth) Huma() func(huma.Context, func(huma.Context)) {
	return func(hctx huma.Context, next func(huma.Context)) {
		values := func(name string) []string {
			var out []string

			hctx.EachHeader(func(n, v string) {
				if strings.EqualFold(n, name) {
					out = append(out, v)
				}
			})

			return out
		}

		ctx, err := k.authenticate(hctx.Context(), hctx.Header, values)
		if err != nil {
			refusal := helper.NewHTTPError(http.StatusUnauthorized, err.Error())
			reqlog.RecordError(hctx.Context(), refusal)
			// the headers first: setting the status sends them.
			hctx.SetHeader(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
			hctx.SetStatus(http.StatusUnauthorized)
			_ = json.NewEncoder(hctx.BodyWriter()).Encode(helper.ErrorBody{Message: refusal.Message})

			return
		}

		if ctx != hctx.Context() {
			hctx = huma.WithContext(hctx, ctx)
		}

		next(hctx)
	}
}

// authenticate returns the context to give to the handler, or the reason why the request is refused.
// values returns all the values of a header.
func (k KeyAuth) authenticate(
	ctx context.Context, header func(string) string, values func(string) []string,
) (context.Context, error) {
	if k.cfg.Skipper != nil && k.cfg.Skipper() {
		return ctx, nil
	}

	// the X-API-Key header takes precedence: a key that is refused is not tried again as a bearer token.
	if xAPIKey := header(helper.XAPIKeyHeader); xAPIKey != "" {
		if out, ok := k.cfg.Validator(ctx, xAPIKey); ok {
			return out, nil
		}

		return ctx, helper.ErrUnauthorized
	}

	key, err := bearerToken(values(protocol.HeaderAuthorization))
	if err != nil {
		if k.cfg.AllowAnonymous && errors.Is(err, errHeaderMissing) {
			if out, ok := k.cfg.Validator(ctx, ""); ok {
				return out, nil
			}
		}

		return ctx, err
	}

	out, ok := k.cfg.Validator(ctx, key)
	if !ok {
		return ctx, fmt.Errorf("authenticating the API key: %w", errKeyInvalid)
	}

	return out, nil
}

// bearerToken returns the key of the first Authorization header that holds a bearer token.
func bearerToken(values []string) (string, error) {
	if len(values) == 0 {
		return "", errHeaderMissing
	}

	for _, value := range values {
		if len(value) > len(protocol.BearerPrefix) &&
			strings.EqualFold(value[:len(protocol.BearerPrefix)], protocol.BearerPrefix) {
			return value[len(protocol.BearerPrefix):], nil
		}
	}

	return "", errHeaderInvalid
}
