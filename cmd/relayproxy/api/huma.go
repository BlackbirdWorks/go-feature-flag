package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

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
//
// The errors the handlers return are [helper.HTTPError]. The errors huma raises itself (a failure it did not get a
// status error for, which it wraps in its RFC 9457 model) are turned into the same body: huma has no error factory
// per API (its factories are package variables), the transformer of the API is the place that is not global.
func errorTransformer(exposeError bool) huma.Transformer {
	return func(ctx huma.Context, _ string, v any) (any, error) {
		if model, ok := v.(*huma.ErrorModel); ok {
			ctx.SetHeader(helper.HeaderContentType, helper.MIMEApplicationJSON)
			herr := modelError(model)
			reqlog.RecordError(ctx.Context(), herr)
			_, body := helper.ErrorResponse(herr, exposeError)

			return body, nil
		}
		herr, ok := errors.AsType[*helper.HTTPError](asError(v))
		if !ok {
			return v, nil
		}
		_, body := helper.ErrorResponse(herr, exposeError)
		reqlog.RecordError(ctx.Context(), herr)

		return body, nil
	}
}

// modelError is the error of the relay proxy for an error of the RFC 9457 model of huma: the text of the status as
// message, what huma reports as the wrapped error.
func modelError(model *huma.ErrorModel) *helper.HTTPError {
	details := make([]error, 0, len(model.Errors)+1)
	if model.Detail != "" {
		details = append(details, errors.New(model.Detail)) //nolint:err113 // the text huma gives
	}
	for _, d := range model.Errors {
		details = append(details, errors.New(d.Message)) //nolint:err113 // the text huma gives
	}
	if len(details) == 0 {
		return helper.NewHTTPError(model.Status, "")
	}

	return helper.WrapHTTPError(model.Status, errors.Join(details...))
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
