package api

import (
	"net/http"
	"reflect"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/modeldocs"
	"github.com/blackbirdworks/semaphore/notifier"
)

// Tags of the operations.
const (
	tagEvaluation = "Semaphore Evaluation API"
	tagStream     = "Semaphore Evaluation Stream API"
	tagOFREP      = "OpenFeature Remote Evaluation Protocol (OFREP)"
	tagManifest   = "OpenFeature Manisfest API"
	tagAdmin      = "Admin API to manage Semaphore"
	tagMonitoring = "Monitoring"
	tagProfiling  = "Profiling"
)

const (
	inHeader    = "header"
	inQuery     = "query"
	typeString  = "string"
	contentJSON = "application/json"
	etagExample = `Etag: "117-0193435c612c50d93b798619d9464856263dbf9f"`
)

// securityAll is the security of the endpoints that accept an API key in any of the two headers.
func securityAll() []map[string][]string {
	return []map[string][]string{{securityAPIKey: {}}, {securityXAPIKey: {}}}
}

// securityAuthorization is the security of the endpoints that only document the Authorization header.
func securityAuthorization() []map[string][]string {
	return []map[string][]string{{securityAPIKey: {}}}
}

// schemaOf returns the schema of the type of v, registered in the document.
func schemaOf(api huma.API, v any) *huma.Schema {
	return api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(v), true, "")
}

func jsonResponse[T any](api huma.API, description string) *huma.Response {
	return &huma.Response{
		Description: description,
		Content: map[string]*huma.MediaType{
			contentJSON: {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[T](), true, "")},
		},
	}
}

func errorResponse(api huma.API, description string) *huma.Response {
	return jsonResponse[helper.ErrorBody](api, description)
}

func notModified() *huma.Response {
	return &huma.Response{Description: etagExample}
}

func jsonBody(api huma.API, description string, required bool, v any) *huma.RequestBody {
	return &huma.RequestBody{
		Description: description,
		Required:    required,
		Content:     map[string]*huma.MediaType{contentJSON: {Schema: schemaOf(api, v)}},
	}
}

func ifNoneMatch() *huma.Param {
	return &huma.Param{
		Name: "If-None-Match", In: inHeader,
		Description: "The request will be processed only if ETag doesn't match.",
		Schema:      &huma.Schema{Type: typeString},
	}
}

func flagKeyParam() *huma.Param {
	return &huma.Param{
		Name: "flag_key", In: "path", Required: true,
		Description: "Name of your feature flag", Schema: &huma.Schema{Type: typeString},
	}
}

func apiKeyQueryParam() *huma.Param {
	return &huma.Param{
		Name: "apiKey", In: inQuery,
		Description: "apiKey to authorize the connection to the relay proxy", Schema: &huma.Schema{Type: typeString},
	}
}

// setRequestBody replaces the request body huma derived from a RawBody field (a binary document) by the JSON
// document the handler decodes: the handler decodes the bytes itself to keep its error bodies.
func setRequestBody(api huma.API, id string, body *huma.RequestBody) {
	for _, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Post, item.Put, item.Delete, item.Patch} {
			if op != nil && op.OperationID == id {
				op.RequestBody = body
			}
		}
	}
}

func statusKey(code int) string { return strconv.Itoa(code) }

// operationAllFlags documents POST /v1/allflags.
func operationAllFlags(api huma.API) *huma.Operation {
	return &huma.Operation{
		OperationID: "allFlags",
		Method:      http.MethodPost,
		Path:        "/v1/allflags",
		Summary:     "All flags variations for a user",
		Tags:        []string{tagEvaluation},
		Security:    securityAll(),
		Description: "Making a **POST** request to the URL `/v1/allflags` will give you the values of all the flags for\n" +
			"this user.\n\nTo get a variation you should provide information about the user.\n" +
			"For that you should provide some user information in JSON in the request body.",
		RequestBody: jsonBody(api, "Evaluation context to challenge against the flags.", true, model.AllFlagRequest{}),
		Responses: map[string]*huma.Response{
			statusKey(http.StatusOK):                  jsonResponse[modeldocs.AllFlags](api, "Success"),
			statusKey(http.StatusBadRequest):          errorResponse(api, "Bad Request"),
			statusKey(http.StatusInternalServerError): errorResponse(api, "Internal server error"),
		},
	}
}

// operationFlagEval documents POST /v1/feature/{flag_key}/eval.
func operationFlagEval(api huma.API) *huma.Operation {
	return &huma.Operation{
		OperationID: "flagEval",
		Method:      http.MethodPost,
		Path:        "/v1/feature/{flag_key}/eval",
		Summary:     "Evaluate a feature flag",
		Tags:        []string{tagEvaluation},
		Security:    securityAll(),
		Description: "Making a **POST** request to the URL `/v1/feature/<your_flag_name>/eval` will give you the value of\n" +
			"the\n" +
			"flag for this user.\n\nTo get a variation you should provide information about the user:\n" +
			"- User information in JSON in the request body.\n" +
			"- A default value in case there is an error while evaluating the flag.\n\n" +
			"Note that you will always have a usable value in the response, you can use the field `failed` to know if\n" +
			"an issue has occurred during the validation of the flag, in that case the value returned will be the\n" +
			"default value.",
		Parameters:  []*huma.Param{flagKeyParam()},
		RequestBody: jsonBody(api, "Evaluation context and default value for the flag.", true, model.EvalFlagRequest{}),
		Responses: map[string]*huma.Response{
			statusKey(http.StatusOK):                  jsonResponse[modeldocs.EvalFlagDoc](api, "Success"),
			statusKey(http.StatusBadRequest):          errorResponse(api, "Bad Request"),
			statusKey(http.StatusInternalServerError): errorResponse(api, "Internal server error"),
		},
	}
}

// operationOFREPEval documents POST /ofrep/v1/evaluate/flags/{flag_key}.
func operationOFREPEval(api huma.API) *huma.Operation {
	return &huma.Operation{
		OperationID: "ofrepEvaluate",
		Method:      http.MethodPost,
		Path:        "/ofrep/v1/evaluate/flags/{flag_key}",
		Summary:     "Evaluate a feature flag using the OpenFeature Remote Evaluation Protocol",
		Tags:        []string{tagOFREP},
		Security:    securityAll(),
		Description: "Making a **POST** request to the URL `/ofrep/v1/evaluate/flags/{your_flag_name}` will give you the\n" +
			"value of the flag for this evaluation context",
		Parameters:  []*huma.Param{flagKeyParam()},
		RequestBody: jsonBody(api, "Evaluation Context for this API call", true, model.OFREPEvalFlagRequest{}),
		Responses: map[string]*huma.Response{
			statusKey(http.StatusOK): jsonResponse[model.OFREPEvaluateSuccessResponse](api, "Success"),
			statusKey(http.StatusBadRequest): jsonResponse[model.OFREPEvaluateResponseError](
				api,
				"Bad Request",
			),
			statusKey(http.StatusUnauthorized): errorResponse(api, "Unauthorized"),
			statusKey(http.StatusNotFound): jsonResponse[model.OFREPEvaluateResponseError](
				api,
				"Flag Not Found",
			),
			statusKey(http.StatusInternalServerError): errorResponse(api, "Internal server error"),
		},
	}
}

// operationOFREPBulk documents POST /ofrep/v1/evaluate/flags.
func operationOFREPBulk(api huma.API) *huma.Operation {
	return &huma.Operation{
		OperationID: "ofrepBulkEvaluate",
		Method:      http.MethodPost,
		Path:        "/ofrep/v1/evaluate/flags",
		Summary:     "Open-Feature Remote Evaluation Protocol bulk evaluation API.",
		Tags:        []string{tagOFREP},
		Security:    securityAll(),
		Description: "Making a **POST** request to the URL `/ofrep/v1/evaluate/flags` will give you the value of the list\n" +
			"of feature flags for this evaluation context.\n\n" +
			"If no flags are provided, the API will evaluate all available flags in the configuration.",
		Parameters: []*huma.Param{ifNoneMatch()},
		RequestBody: jsonBody(api, "Evaluation Context and list of flag for this API call", true,
			model.OFREPEvalFlagRequest{}),
		Responses: map[string]*huma.Response{
			statusKey(http.StatusOK): jsonResponse[model.OFREPBulkEvaluateSuccessResponse](
				api,
				"OFREP successful evaluation response",
			),
			statusKey(http.StatusNotModified): notModified(),
			statusKey(http.StatusBadRequest): jsonResponse[model.OFREPCommonResponseError](
				api,
				"Bad evaluation request",
			),
			statusKey(http.StatusUnauthorized): errorResponse(
				api,
				"Unauthorized - You need credentials to access the API",
			),
			statusKey(http.StatusForbidden): errorResponse(
				api,
				"Forbidden - You are not authorized to access the API",
			),
			statusKey(http.StatusInternalServerError): errorResponse(api, "Internal server error"),
		},
	}
}

// operationValidate documents POST /admin/v1/flags/validate.
func operationValidate(api huma.API) *huma.Operation {
	content := map[string]*huma.MediaType{}
	for _, ct := range []string{
		contentJSON, "application/yaml", "application/x-yaml", "text/yaml", "application/toml",
	} {
		content[ct] = &huma.MediaType{Schema: &huma.Schema{Type: "object"}}
	}

	return &huma.Operation{
		OperationID: "validateFlags", Method: http.MethodPost, Path: "/admin/v1/flags/validate",
		Summary: "Validate a flag file", Tags: []string{tagAdmin}, Security: securityAuthorization(),
		Description: "Validates the flag file sent in the body of the request, using the same rules as the\n" +
			"`goff lint` command.\n" +
			"The flag file is not loaded, nothing is changed on the flags served by the relay proxy.\n" +
			"The format of the body is selected with the `Content-Type` header (`application/json`,\n" +
			"`application/yaml`, `application/x-yaml`, `text/yaml` or `application/toml`).\n" +
			"The maximum size of the body is configurable per format with `endpoints.validateFlags`\n" +
			"(defaults: 1 MiB for JSON, 64 KiB for YAML and TOML).",
		RequestBody: &huma.RequestBody{Description: "The flag file to validate", Required: true, Content: content},
		Responses: map[string]*huma.Response{
			statusKey(http.StatusOK): jsonResponse[controller.FlagValidationResponse](
				api,
				"All the flags are valid",
			),
			statusKey(http.StatusBadRequest):            errorResponse(api, "Unparseable body"),
			statusKey(http.StatusRequestEntityTooLarge): errorResponse(api, "Body too large"),
			statusKey(http.StatusUnsupportedMediaType):  errorResponse(api, "Missing or unsupported content type"),
			statusKey(http.StatusUnprocessableEntity): jsonResponse[controller.FlagValidationResponse](
				api,
				"At least one flag is invalid",
			),
		},
	}
}

// operationSSE documents GET /stream/v1/sse/flag/change.
func operationSSE(api huma.API) *huma.Operation {
	return &huma.Operation{
		OperationID: "sseFlagChange",
		Method:      http.MethodGet,
		Path:        "/stream/v1/sse/flag/change",
		Summary:     "SSE endpoint to be notified about flag changes",
		Tags:        []string{tagStream},
		Description: "Server-Sent Events endpoint pushing flag change notifications, following OpenFeature ADR-0008.\n" +
			"It is advertised in the `eventStreams` field of the OFREP bulk evaluation response.\n" +
			"Each event is sent as `event: message` with a `model.OFREPSSEEvent` JSON payload,\n" +
			"a `refetchEvaluation` event means that the provider must re-fetch its evaluations.\n" +
			"The stream also sends SSE comment lines (`: connected` when the connection opens,\n" +
			"`: heartbeat` every 30 seconds), they are ignored by SSE clients.\n" +
			"The full URL (including query string) is sensitive and must not be logged\n" +
			"or persisted by intermediaries.",
		Parameters: []*huma.Param{apiKeyQueryParam()},
		Responses: map[string]*huma.Response{
			statusKey(http.StatusOK): {
				Description: "SSE stream of OFREP flag change events",
				Content: map[string]*huma.MediaType{
					"text/event-stream": {Schema: schemaOf(api, model.OFREPSSEEvent{})},
				},
			},
			statusKey(http.StatusBadRequest):          errorResponse(api, "Bad Request"),
			statusKey(http.StatusUnauthorized):        errorResponse(api, "Unauthorized"),
			statusKey(http.StatusInternalServerError): errorResponse(api, "Internal server error"),
		},
	}
}

// operationWebsocket documents GET /stream/v1/ws/flag/change, and its deprecated alias GET /ws/v1/flag/change.
func operationWebsocket(api huma.API, deprecated bool) *huma.Operation {
	op := &huma.Operation{
		OperationID: "wsFlagChange", Method: http.MethodGet, Path: "/stream/v1/ws/flag/change",
		Summary: "Websocket endpoint to be notified about flag changes", Tags: []string{tagStream},
		Description: "This endpoint is a websocket endpoint to be notified about flag changes;\n" +
			"every change pushes a notifier.DiffCache message to the client.\n" +
			"When disableFlagDetailsInStream is true, values are empty objects.",
		Parameters: []*huma.Param{apiKeyQueryParam()},
		Responses: map[string]*huma.Response{
			statusKey(http.StatusOK):                  jsonResponse[notifier.DiffCache](api, "Success"),
			statusKey(http.StatusBadRequest):          errorResponse(api, "Bad Request"),
			statusKey(http.StatusUnauthorized):        errorResponse(api, "Unauthorized"),
			statusKey(http.StatusInternalServerError): errorResponse(api, "Internal server error"),
		},
	}
	if deprecated {
		op.OperationID = "wsFlagChangeLegacy"
		op.Path = "/ws/v1/flag/change"
		op.Deprecated = true
		op.Description = "Deprecated: use /stream/v1/ws/flag/change instead. This endpoint\n" +
			"is a websocket endpoint to be notified about flag changes; every\n" +
			"change pushes a notifier.DiffCache message to the client.\n" +
			"When disableFlagDetailsInStream is true, values are empty objects."
	}

	return op
}

func operationMetrics() *huma.Operation {
	return &huma.Operation{
		OperationID: "metrics", Method: http.MethodGet, Path: "/metrics",
		Summary: "Prometheus endpoint", Tags: []string{tagMonitoring},
		Description: "This endpoint is providing metrics about the relay proxy in the prometheus format.",
		Responses: map[string]*huma.Response{
			statusKey(http.StatusOK): {
				Description: "OK",
				Content:     map[string]*huma.MediaType{"text/plain": {Schema: &huma.Schema{Type: typeString}}},
			},
		},
	}
}

func operationPprof() *huma.Operation {
	return &huma.Operation{
		OperationID: "pprof", Method: http.MethodGet, Path: "/debug/pprof/",
		Summary: "pprof endpoint", Tags: []string{tagProfiling},
		Description: "This endpoint is provided by the standard library pprof handlers.\n" +
			"To know more please check this blogpost from the GO team https://go.dev/blog/pprof.\n" +
			"Visit the page /debug/pprof/ to see the available endpoints, all endpoint are not in the OpenAPI " +
			"documentation because they are standard pprof endpoints.\n" +
			"This endpoint is only available in debug mode.",
		Responses: map[string]*huma.Response{
			statusKey(http.StatusOK): {
				Description: "OK",
				Content:     map[string]*huma.MediaType{"text/plain": {Schema: &huma.Schema{Type: typeString}}},
			},
		},
	}
}

// typedOperations are the operations huma serves (typed input and output). Their descriptions are here, the
// parameters and the responses come from the input and output types.
func operationHealth() huma.Operation {
	return huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/health",
		Summary:     "Health",
		Tags:        []string{tagMonitoring},
		Description: "Making a **GET** request to the URL path `/health` will tell you if the relay proxy is ready to\n" +
			"serve\n" +
			"traffic.\n\nThis is useful especially for loadbalancer to know that they can send traffic to the service.",
	}
}

func operationInfo() huma.Operation {
	return huma.Operation{
		OperationID: "info",
		Method:      http.MethodGet,
		Path:        "/info",
		Summary:     "Info",
		Tags:        []string{tagMonitoring},
		Description: "Making a **GET** request to the URL path `/info` will give you information about the actual state\n" +
			"of the relay proxy.\n\nAs of Today the level of information is small be we can improve this endpoint to\n" +
			"returns more\n" +
			"information.",
	}
}

func operationRefresh() huma.Operation {
	return huma.Operation{
		OperationID: "retrieverRefresh", Method: http.MethodPost, Path: "/admin/v1/retriever/refresh",
		Summary: "This endpoint is used to force the refresh of the flags in the cache.", Tags: []string{tagAdmin},
		Security: securityAuthorization(),
		Description: "This endpoint is used to force the refresh of the flags in the cache.\n" +
			"This endpoint is used when you know explicitly that a flag has changed, and you want to trigger\n" +
			"the collection of the new versions.",
		Errors: []int{http.StatusBadRequest, http.StatusInternalServerError},
	}
}

func operationManifest() huma.Operation {
	return huma.Operation{
		OperationID: "getManifest",
		Method:      http.MethodGet,
		Path:        "/openfeature/v0/manifest",
		Summary:     "GetManifest is a GET endpoint to return the flags configured in Semaphore.",
		Tags:        []string{tagManifest},
		Security:    securityAuthorization(),
		Description: "**GET** request to the URL `/openfeature/v0/manifest` returns the project manifest\n" +
			"containing active flags. Archived flags are excluded.",
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
	}
}

func operationFlagChange() huma.Operation {
	return huma.Operation{
		OperationID: "flagChange", Method: http.MethodGet, Path: "/v1/flag/change",
		Summary: "Endpoint to poll if you want to know if there is a configuration change in the flags",
		Tags:    []string{tagEvaluation}, Security: securityAll(),
		Description: "Making a **GET** request to the URL `/v1/flag/change` will give you the hash of the current\n" +
			"configuration, you can use this hash to know if the configuration has changed.",
		Responses: map[string]*huma.Response{statusKey(http.StatusNotModified): notModified()},
		Errors:    []int{http.StatusInternalServerError},
	}
}

func operationFlagConfiguration() huma.Operation {
	return huma.Operation{
		OperationID: "flagConfiguration", Method: http.MethodPost, Path: "/v1/flag/configuration",
		Summary: "Endpoint to poll if you want to get the configuration of the flags.",
		Tags:    []string{tagEvaluation}, Security: securityAll(),
		Description: "Making a **POST** request to the URL `/v1/flag/configuration` will give you the list of\n" +
			"the flags to use them for local evaluation in your provider.",
		Responses: map[string]*huma.Response{statusKey(http.StatusNotModified): notModified()},
		Errors:    []int{http.StatusInternalServerError},
	}
}

func operationCollector() huma.Operation {
	return huma.Operation{
		OperationID: "collectEvalData",
		Method:      http.MethodPost,
		Path:        "/v1/data/collector",
		Summary:     "Endpoint to send usage of your flags to be collected",
		Tags:        []string{tagEvaluation},
		Security:    securityAll(),
		Description: "This endpoint is receiving the events of your flags usage to send them in the data collector.\n\n" +
			"It is used by the different Open Feature providers to send in bulk all the cached events to avoid\n" +
			"to lose track of what happen when a cached flag is used.",
		Errors: []int{http.StatusBadRequest, http.StatusInternalServerError},
	}
}
