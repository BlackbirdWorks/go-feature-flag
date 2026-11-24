package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	reqlog "github.com/blackbirdworks/semaphore/cmdhelpers/api/middleware"
)

const (
	// securityAPIKey and securityXAPIKey are the names of the security schemes of the document.
	securityAPIKey  = "ApiKeyAuth"  //nolint:gosec // the name of a security scheme, not a credential
	securityXAPIKey = "XApiKeyAuth" //nolint:gosec // the name of a security scheme, not a credential
	// schemeAPIKey is the type of the security schemes: an API key.
	schemeAPIKey = "apiKey"
)

// installHumaErrors makes huma build its own errors (the ones it raises itself: a parameter that is refused, ...)
// with the error model of the relay proxy instead of RFC 9457. huma holds the factories in package variables, so
// they are set once, whatever the number of servers.
var installHumaErrors = sync.OnceFunc(func() { //nolint:gochecknoglobals // huma has no per-API error factory
	//nolint:reassign // huma has no per-API error factory
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		return newHTTPError(status, msg, errs...)
	}
	huma.NewErrorWithContext = func( //nolint:reassign // huma has no per-API error factory
		_ huma.Context, status int, msg string, errs ...error,
	) huma.StatusError {
		return newHTTPError(status, msg, errs...)
	}
})

func newHTTPError(status int, msg string, errs ...error) *helper.HTTPError {
	if len(errs) > 0 {
		return helper.WrapHTTPError(status, errors.Join(errs...))
	}

	return helper.NewHTTPError(status, msg)
}

// introduction is the description of the API, the first paragraphs of the document.
const introduction = `# Introduction

This API is documented in **OpenAPI format** and describe the REST API of the **Semaphore relay proxy**.

The relay proxy is a component to evaluate your feature flags remotely when using **Semaphore**.  
This API is mostly used by all the OpenFeature providers.`

// jsonFormat is the JSON format of the relay proxy: the encoding of encoding/json, the one of the responses
// before huma (HTML characters escaped, a newline after the document).
func jsonFormat() huma.Format {
	return huma.Format{
		Marshal: func(w io.Writer, v any) error {
			return json.NewEncoder(w).Encode(v)
		},
		Unmarshal: json.Unmarshal,
	}
}

// errorTransformer gives the bodies of the errors the shape of the relay proxy, and reports the error to the
// access log. exposeError adds the wrapped error to the body, at the debug level.
func errorTransformer(exposeError bool) huma.Transformer {
	return func(ctx huma.Context, _ string, v any) (any, error) {
		var herr *helper.HTTPError
		if !errors.As(asError(v), &herr) {
			return v, nil
		}
		_, body := helper.ErrorResponse(herr, exposeError)
		reqlog.RecordError(ctx.Context(), herr)

		return body, nil
	}
}

func asError(v any) error {
	err, _ := v.(error)

	return err
}

// newHumaAPI returns the huma API that holds the operations of the relay proxy. The document is generated from
// them. huma serves nothing by itself (no document, no docs page): the relay proxy owns its routes.
func newHumaAPI(
	mux *http.ServeMux,
	exposeError, docs bool,
) huma.API {
	installHumaErrors()

	cfg := huma.DefaultConfig("Semaphore relay proxy endpoints", "1.0.0")
	// huma serves the document and the Swagger UI under the swagger path, only when it is enabled: the relay proxy
	// owns every other route.
	cfg.OpenAPIPath, cfg.DocsPath, cfg.SchemasPath = "", "", ""
	if docs {
		cfg.OpenAPIPath, cfg.DocsPath = "/swagger/openapi", "/swagger/index.html"
		cfg.DocsRenderer = huma.DocsRendererSwaggerUI
	}
	cfg.CreateHooks = nil
	cfg.Formats = map[string]huma.Format{"application/json": jsonFormat(), "json": jsonFormat()}
	cfg.DefaultFormat = "application/json"
	cfg.Transformers = []huma.Transformer{errorTransformer(exposeError)}
	cfg.Info.Description = introduction
	cfg.Info.Contact = &huma.Contact{Name: "Semaphore relay proxy"}
	cfg.Info.License = &huma.License{
		Name: "MIT",
		URL:  "https://github.com/BlackbirdWorks/semaphore/blob/main/LICENSE",
	}
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		securityAPIKey: {
			Type: "apiKey",
			In:   "header",
			Name: "Authorization",
			Description: "Use configured APIKeys in yaml config as authorization keys, " +
				"disabled when this yaml config is not set.",
		},
		securityXAPIKey: {
			Type: schemeAPIKey, In: inHeader, Name: "X-API-Key",
			Description: "Use configured APIKeys in yaml config as authorization keys via X-API-Key header,\n" +
				"disabled when this yaml config is not set.",
		},
	}

	return humago.New(mux, cfg)
}
