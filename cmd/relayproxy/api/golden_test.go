package api_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/testhelpers"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	accessControlRequestHeaders = "Access-Control-Request-Headers"
	accessControlRequestMethod  = "Access-Control-Request-Method"
	adminV1Flags                = "/admin/v1/flags/validate"
	adminV1Retriever            = "/admin/v1/retriever/refresh"
	bodyNotRecordedN            = "(body not recorded)\n"
	debugPprof                  = "/debug/pprof/"
	goldenUpdate                = "GOLDEN_UPDATE"
	healthPath                  = "/health"
	httpsExampleCom             = "https://example.com"
	metricsName                 = "metrics"
	nope                        = "/nope"
	ofrepEvaluateNope           = "/ofrep/v1/evaluate/flags/nope"
	ofrepEvaluateTestFlag       = "/ofrep/v1/evaluate/flags/test-flag"
	openfeatureV0Manifest       = "/openfeature/v0/manifest"
	streamV1Sse                 = "/stream/v1/sse/flag/change"
	streamV1Ws                  = "/stream/v1/ws/flag/change"
	swaggerIndexHTML            = "/swagger/index.html"
	textPlain                   = "text/plain"
	v1Allflags                  = "/v1/allflags"
	v1DataCollector             = "/v1/data/collector"
	v1FeatureTest               = "/v1/feature/test-flag/eval"
	v1FlagChange                = "/v1/flag/change"
	v1FlagConfiguration         = "/v1/flag/configuration"
)

// The golden tests pin what the relay proxy sends for every route and for the requests that no route (or no
// method of a route) serves: status, the headers that are part of the contract and the body. The files in
// testdata/golden were captured from the echo implementation, the router migration must not change them.
// GOLDEN_UPDATE=1 rewrites them.

const (
	goldenDir      = "testdata/golden"
	goldenVersion  = "v0.0.0-golden"
	goldenEvalKey  = "golden-eval-key"
	goldenAdminKey = "golden-admin-key"
	flagBody       = `{"evaluationContext":{"key":"user-1"},"defaultValue":false}`
)

type goldenCase struct {
	headers map[string]string
	name    string
	method  string
	path    string
	body    string
	// followETag sends the request twice: the second one carries the ETag of the first as If-None-Match, and the
	// second response is the one recorded.
	followETag bool
	// headersOnly records the status and the headers, the body is a stream that never ends.
	headersOnly bool
	// noBody records the status and the headers only.
	noBody bool
}

func jsonHeaders() map[string]string {
	return map[string]string{protocol.HeaderContentType: protocol.MIMEApplicationJSON}
}

func withHeaders(extra map[string]string) map[string]string {
	h := jsonHeaders()
	maps.Copy(h, extra)

	return h
}

// goldenOpenCases are served by a relay proxy that has no API keys.
func goldenOpenCases() []goldenCase {
	return slices.Concat(
		goldenOpenSuccessCases(),
		goldenOpenErrorCases(),
		goldenOpenRoutingAndMethodCases(),
	)
}

func goldenOpenSuccessCases() []goldenCase {
	return []goldenCase{
		// the 17 routes, success
		{name: "allflags_ok", method: http.MethodPost, path: v1Allflags, body: flagBody, headers: jsonHeaders()},
		{
			name: "allflags_no_etag", method: http.MethodPost, path: v1Allflags, body: flagBody,
			headers: withHeaders(map[string]string{protocol.HeaderIfNoneMatch: `"x"`}),
		},
		{
			name: "eval_ok", method: http.MethodPost, path: v1FeatureTest, body: flagBody,
			headers: jsonHeaders(),
		},
		{
			name: "eval_unknown_flag", method: http.MethodPost, path: "/v1/feature/nope/eval", body: flagBody,
			headers: jsonHeaders(),
		},
		{
			name:   "collector_ok",
			method: http.MethodPost,
			path:   v1DataCollector,
			body: `{"meta":{"app":"x"},"events":[{"kind":"feature","contextKind":"user","userKey":"u",` +
				`"creationDate":1,"key":"test-flag","variation":"True","value":true,"default":false}]}`,
			headers: jsonHeaders(),
		},
		{name: "flag_change_ok", method: http.MethodGet, path: v1FlagChange},
		{name: "flag_change_304", method: http.MethodGet, path: v1FlagChange, followETag: true},
		{
			name:    "configuration_ok",
			method:  http.MethodPost,
			path:    v1FlagConfiguration,
			body:    `{"flags":["test-flag"]}`,
			headers: jsonHeaders(),
		},
		{
			name: "configuration_all", method: http.MethodPost, path: v1FlagConfiguration, headers: jsonHeaders(),
		},
		{
			name: "configuration_304", method: http.MethodPost, path: v1FlagConfiguration,
			headers: jsonHeaders(), followETag: true,
		},
		{name: "swagger_index", method: http.MethodGet, path: swaggerIndexHTML, headers: nil},
		{name: "swagger_dir_slash", method: http.MethodGet, path: "/swagger/"},
		{name: "swagger_openapi", method: http.MethodGet, path: swaggerOpenapiJSON, headers: nil},
		{
			name:    "ofrep_bulk_ok",
			method:  http.MethodPost,
			path:    fixtures.OFREPEvaluateFlagsPath,
			body:    contextTargetingKeyUserOne,
			headers: jsonHeaders(),
		},
		{
			name:       "ofrep_bulk_304",
			method:     http.MethodPost,
			path:       fixtures.OFREPEvaluateFlagsPath,
			body:       contextTargetingKeyUserOne,
			headers:    jsonHeaders(),
			followETag: true,
		},
		{
			name:    "ofrep_eval_ok",
			method:  http.MethodPost,
			path:    ofrepEvaluateTestFlag,
			body:    contextTargetingKeyUserOne,
			headers: jsonHeaders(),
		},
		{
			name: "ws_no_upgrade", method: http.MethodGet, path: streamV1Ws,
		},
		{
			name: "sse_ok", method: http.MethodGet, path: streamV1Sse, headersOnly: true,
		},
		{name: "ws_legacy_no_upgrade", method: http.MethodGet, path: fixtures.WebsocketFlagChangePath},
		{name: metricsName, method: http.MethodGet, path: metricsPath, noBody: true},
		{name: "health", method: http.MethodGet, path: healthPath},
		{name: "info", method: http.MethodGet, path: info},
		{name: "manifest_ok", method: http.MethodGet, path: openfeatureV0Manifest},

		// admin routes (no key configured: the admin routes refuse everybody)
		{name: "admin_refresh_noauth", method: http.MethodPost, path: adminV1Retriever},
		{
			name: "admin_validate_noauth", method: http.MethodPost, path: adminV1Flags,
			headers: jsonHeaders(), body: `{}`,
		},
	}
}

func goldenOpenErrorCases() []goldenCase {
	return []goldenCase{
		// error bodies of the handlers
		{
			name: "allflags_bad_json", method: http.MethodPost, path: v1Allflags, body: `{"evaluationContext":`,
			headers: jsonHeaders(),
		},
		{
			name:    "allflags_wrong_type",
			method:  http.MethodPost,
			path:    v1Allflags,
			body:    `{"evaluationContext":42}`,
			headers: jsonHeaders(),
		},
		{
			name: "allflags_no_context", method: http.MethodPost, path: v1Allflags, body: `{}`,
			headers: jsonHeaders(),
		},
		{
			name: "allflags_empty_body", method: http.MethodPost, path: v1Allflags, headers: jsonHeaders(),
		},
		{
			name: "allflags_legacy_user", method: http.MethodPost, path: v1Allflags,
			body: `{"user":{"key":"u"}}`, headers: jsonHeaders(),
		},
		{
			name: "allflags_text_plain", method: http.MethodPost, path: v1Allflags, body: flagBody,
			headers: map[string]string{protocol.HeaderContentType: textPlain},
		},
		{
			name: "allflags_charset", method: http.MethodPost, path: v1Allflags, body: flagBody,
			headers: map[string]string{protocol.HeaderContentType: "application/json; charset=utf-8"},
		},
		{
			name: "allflags_accept_html", method: http.MethodPost, path: v1Allflags, body: flagBody,
			headers: withHeaders(map[string]string{"Accept": "text/html"}),
		},
		{
			name: "allflags_unknown_field", method: http.MethodPost, path: v1Allflags,
			body: `{"evaluationContext":{"key":"u"},"extra":1}`, headers: jsonHeaders(),
		},
		{
			name: "eval_bad_json", method: http.MethodPost, path: v1FeatureTest, body: `nope`,
			headers: jsonHeaders(),
		},
		{
			name: "eval_no_context", method: http.MethodPost, path: v1FeatureTest, body: `{}`,
			headers: jsonHeaders(),
		},
		{
			name: "eval_escaped_key", method: http.MethodPost, path: "/v1/feature/test%2Dflag/eval", body: flagBody,
			headers: jsonHeaders(),
		},
		{
			name: "eval_escaped_slash_key", method: http.MethodPost, path: "/v1/feature/a%2Fb/eval", body: flagBody,
			headers: jsonHeaders(),
		},
		{
			name: "eval_escaped_colon_key", method: http.MethodPost, path: "/v1/feature/a%3Ab/eval", body: flagBody,
			headers: jsonHeaders(),
		},
		{
			name: "eval_unicode_key", method: http.MethodPost, path: "/v1/feature/%C3%A9t%C3%A9/eval", body: flagBody,
			headers: jsonHeaders(),
		},
		{
			name: "eval_empty_key", method: http.MethodPost, path: "/v1/feature//eval", body: flagBody,
			headers: jsonHeaders(),
		},
		{
			name: "eval_trailing_slash", method: http.MethodPost, path: "/v1/feature/test-flag/eval/", body: flagBody,
			headers: jsonHeaders(),
		},
		{
			name: "eval_extra_segment", method: http.MethodPost, path: "/v1/feature/a/b/eval", body: flagBody,
			headers: jsonHeaders(),
		},
		{
			name: "collector_bad_json", method: http.MethodPost, path: v1DataCollector, body: `[`,
			headers: jsonHeaders(),
		},
		{
			name: "collector_no_events", method: http.MethodPost, path: v1DataCollector, body: `{}`,
			headers: jsonHeaders(),
		},
		{
			name: "configuration_bad_json", method: http.MethodPost, path: v1FlagConfiguration, body: `{`,
			headers: jsonHeaders(),
		},
		{
			name: "ofrep_bulk_bad_json", method: http.MethodPost, path: fixtures.OFREPEvaluateFlagsPath, body: `{`,
			headers: jsonHeaders(),
		},
		{
			name: "ofrep_bulk_no_context", method: http.MethodPost, path: fixtures.OFREPEvaluateFlagsPath, body: `{}`,
			headers: jsonHeaders(),
		},
		{
			name:    "ofrep_bulk_empty_body",
			method:  http.MethodPost,
			path:    fixtures.OFREPEvaluateFlagsPath,
			headers: jsonHeaders(),
		},
		{
			name:    "ofrep_bulk_text_plain",
			method:  http.MethodPost,
			path:    fixtures.OFREPEvaluateFlagsPath,
			body:    contextTargetingKeyUserOne,
			headers: map[string]string{protocol.HeaderContentType: textPlain},
		},
		{
			name: "ofrep_bulk_flag_list", method: http.MethodPost, path: fixtures.OFREPEvaluateFlagsPath,
			body: `{"context":{"targetingKey":"u","flags":["test-flag"]}}`, headers: jsonHeaders(),
		},
		{
			name:    "ofrep_eval_empty_key",
			method:  http.MethodPost,
			path:    "/ofrep/v1/evaluate/flags/",
			body:    contextTargetingKeyUserOne,
			headers: jsonHeaders(),
		},
		{
			name:    "ofrep_eval_not_found",
			method:  http.MethodPost,
			path:    ofrepEvaluateNope,
			body:    contextTargetingKeyUserOne,
			headers: jsonHeaders(),
		},
		{
			name: "ofrep_eval_bad_json", method: http.MethodPost, path: ofrepEvaluateTestFlag, body: `{`,
			headers: jsonHeaders(),
		},
		{
			name:    "ofrep_eval_no_context",
			method:  http.MethodPost,
			path:    ofrepEvaluateTestFlag,
			body:    `{}`,
			headers: jsonHeaders(),
		},
		{
			name: "ofrep_eval_escaped_key", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags/test%2Dflag",
			body: contextTargetingKeyUserOne, headers: jsonHeaders(),
		},
		{
			name:    "ofrep_eval_no_etag",
			method:  http.MethodPost,
			path:    ofrepEvaluateTestFlag,
			body:    contextTargetingKeyUserOne,
			headers: withHeaders(map[string]string{protocol.HeaderIfNoneMatch: `"x"`}),
		},
		{
			name:        "ws_bad_apikey_flagset",
			method:      http.MethodGet,
			path:        "/stream/v1/sse/flag/change?apiKey=zzz",
			headersOnly: true,
		},
	}
}

func goldenOpenRoutingAndMethodCases() []goldenCase {
	return []goldenCase{
		// 404, 405, preflight and the other methods
		{name: "notfound_root", method: http.MethodGet, path: "/"},
		{name: "notfound_unknown", method: http.MethodGet, path: nope},
		{name: "notfound_post_unknown", method: http.MethodPost, path: "/v1/nope", body: `{}`, headers: jsonHeaders()},
		{name: "notfound_prefix_only", method: http.MethodPost, path: "/v1", body: `{}`, headers: jsonHeaders()},
		{name: "notfound_prefix_slash", method: http.MethodPost, path: "/v1/", body: `{}`, headers: jsonHeaders()},
		{name: "notfound_swagger_dir", method: http.MethodGet, path: "/swagger"},
		{name: "swagger_missing_file", method: http.MethodGet, path: "/swagger/nope.css"},
		{
			name:    "trailing_slash_allflags",
			method:  http.MethodPost,
			path:    "/v1/allflags/",
			body:    flagBody,
			headers: jsonHeaders(),
		},
		{name: "trailing_slash_health", method: http.MethodGet, path: "/health/"},
		{
			name:    "double_slash_allflags",
			method:  http.MethodPost,
			path:    "//v1/allflags",
			body:    flagBody,
			headers: jsonHeaders(),
		},
		{
			name:    "dot_segment_allflags",
			method:  http.MethodPost,
			path:    "/v1/./allflags",
			body:    flagBody,
			headers: jsonHeaders(),
		},
		{
			name:    "dotdot_segment_allflags",
			method:  http.MethodPost,
			path:    "/v1/x/../allflags",
			body:    flagBody,
			headers: jsonHeaders(),
		},
		{
			name:    "case_sensitive_path",
			method:  http.MethodPost,
			path:    "/V1/allflags",
			body:    flagBody,
			headers: jsonHeaders(),
		},
		{
			name:    "escaped_path_allflags",
			method:  http.MethodPost,
			path:    "/v1/all%66lags",
			body:    flagBody,
			headers: jsonHeaders(),
		},
		{
			name:    "escaped_slash_prefix",
			method:  http.MethodPost,
			path:    "/v1%2Fallflags",
			body:    flagBody,
			headers: jsonHeaders(),
		},
		{
			name:    "query_string_allflags",
			method:  http.MethodPost,
			path:    "/v1/allflags?x=1",
			body:    flagBody,
			headers: jsonHeaders(),
		},
		{name: "method_get_allflags", method: http.MethodGet, path: v1Allflags},
		{
			name:    "method_put_allflags",
			method:  http.MethodPut,
			path:    v1Allflags,
			body:    flagBody,
			headers: jsonHeaders(),
		},
		{name: "method_delete_health", method: http.MethodDelete, path: healthPath},
		{
			name:    "method_post_flag_change",
			method:  http.MethodPost,
			path:    v1FlagChange,
			body:    `{}`,
			headers: jsonHeaders(),
		},
		{name: "method_get_ofrep_bulk", method: http.MethodGet, path: fixtures.OFREPEvaluateFlagsPath},
		{name: "method_get_ofrep_eval", method: http.MethodGet, path: ofrepEvaluateTestFlag},
		{name: "method_get_admin_refresh", method: http.MethodGet, path: adminV1Retriever},
		{name: "method_post_swagger", method: http.MethodPost, path: swaggerIndexHTML},
		{name: "method_post_manifest", method: http.MethodPost, path: openfeatureV0Manifest},
		{name: "method_post_ws", method: http.MethodPost, path: streamV1Ws},
		{name: "head_health", method: http.MethodHead, path: healthPath},
		{name: "head_flag_change", method: http.MethodHead, path: v1FlagChange},
		{name: "head_allflags", method: http.MethodHead, path: v1Allflags},
		{name: "head_notfound", method: http.MethodHead, path: nope},
		{name: "options_no_origin", method: http.MethodOptions, path: v1Allflags},
		{
			name: "options_preflight", method: http.MethodOptions, path: v1Allflags,
			headers: map[string]string{
				protocol.HeaderOrigin:       httpsExampleCom,
				accessControlRequestMethod:  http.MethodPost,
				accessControlRequestHeaders: contentTypeAuthorization,
			},
		},
		{
			name: "options_preflight_unknown_path", method: http.MethodOptions, path: nope,
			headers: map[string]string{
				protocol.HeaderOrigin: httpsExampleCom, accessControlRequestMethod: http.MethodGet,
			},
		},
		{
			name: "cors_simple", method: http.MethodGet, path: healthPath,
			headers: map[string]string{protocol.HeaderOrigin: httpsExampleCom},
		},
		{name: "options_health_no_origin", method: http.MethodOptions, path: healthPath},
		{
			name: "options_health_preflight", method: http.MethodOptions, path: healthPath,
			headers: map[string]string{
				protocol.HeaderOrigin: httpsExampleCom, accessControlRequestMethod: http.MethodGet,
			},
		},
		{
			name: "options_health_origin_only", method: http.MethodOptions, path: healthPath,
			headers: map[string]string{protocol.HeaderOrigin: httpsExampleCom},
		},
		{
			name: "options_swagger_preflight", method: http.MethodOptions, path: swaggerIndexHTML,
			headers: map[string]string{
				protocol.HeaderOrigin: httpsExampleCom, accessControlRequestMethod: http.MethodGet,
				accessControlRequestHeaders: "x-api-key",
			},
		},
		{name: "head_info", method: http.MethodHead, path: info},
		{name: "head_manifest", method: http.MethodHead, path: openfeatureV0Manifest},
		{name: "info_trailing_slash", method: http.MethodGet, path: "/info/"},
		{name: "metrics_post", method: http.MethodPost, path: metricsPath},
		{
			name: "cors_simple_error", method: http.MethodGet, path: nope,
			headers: map[string]string{protocol.HeaderOrigin: httpsExampleCom},
		},
		{name: "trace_method", method: http.MethodTrace, path: healthPath},
		{name: "connect_method", method: http.MethodConnect, path: healthPath},
		{name: "patch_allflags", method: http.MethodPatch, path: v1Allflags, body: `{}`, headers: jsonHeaders()},
		{name: "pprof_disabled", method: http.MethodGet, path: debugPprof},
	}
}

// goldenAuthCases are served by a relay proxy with an evaluation key and an admin key.
func goldenAuthCases() []goldenCase {
	bearer := func(k string) map[string]string {
		return map[string]string{
			protocol.HeaderAuthorization: protocol.BearerPrefix + k,
			protocol.HeaderContentType:   protocol.MIMEApplicationJSON,
		}
	}
	apiKey := func(k string) map[string]string {
		return map[string]string{"X-API-Key": k, protocol.HeaderContentType: protocol.MIMEApplicationJSON}
	}

	return []goldenCase{
		{
			name:    "allflags_no_key",
			method:  http.MethodPost,
			path:    v1Allflags,
			body:    flagBody,
			headers: jsonHeaders(),
		},
		{
			name:    "allflags_bearer_ok",
			method:  http.MethodPost,
			path:    v1Allflags,
			body:    flagBody,
			headers: bearer(goldenEvalKey),
		},
		{
			name:   "allflags_bearer_lower",
			method: http.MethodPost,
			path:   v1Allflags,
			body:   flagBody,
			headers: map[string]string{
				protocol.HeaderAuthorization: "bearer " + goldenEvalKey,
				protocol.HeaderContentType:   protocol.MIMEApplicationJSON,
			},
		},
		{
			name:    "allflags_bearer_bad",
			method:  http.MethodPost,
			path:    v1Allflags,
			body:    flagBody,
			headers: bearer(fixtures.PlaceholderZzz),
		},
		{
			name:   "allflags_basic_scheme",
			method: http.MethodPost,
			path:   v1Allflags,
			body:   flagBody,
			headers: map[string]string{
				protocol.HeaderAuthorization: "Basic abc",
				protocol.HeaderContentType:   protocol.MIMEApplicationJSON,
			},
		},
		{
			name:   "allflags_raw_authorization",
			method: http.MethodPost,
			path:   v1Allflags,
			body:   flagBody,
			headers: map[string]string{
				protocol.HeaderAuthorization: goldenEvalKey,
				protocol.HeaderContentType:   protocol.MIMEApplicationJSON,
			},
		},
		{
			name:   "allflags_bearer_empty",
			method: http.MethodPost,
			path:   v1Allflags,
			body:   flagBody,
			headers: map[string]string{
				protocol.HeaderAuthorization: protocol.BearerPrefix,
				protocol.HeaderContentType:   protocol.MIMEApplicationJSON,
			},
		},
		{
			name:    "allflags_xapikey_ok",
			method:  http.MethodPost,
			path:    v1Allflags,
			body:    flagBody,
			headers: apiKey(goldenEvalKey),
		},
		{
			name:    "allflags_xapikey_bad",
			method:  http.MethodPost,
			path:    v1Allflags,
			body:    flagBody,
			headers: apiKey(fixtures.PlaceholderZzz),
		},
		{
			name: "allflags_xapikey_bad_bearer_ok", method: http.MethodPost, path: v1Allflags, body: flagBody,
			headers: map[string]string{
				"X-API-Key":                  fixtures.PlaceholderZzz,
				protocol.HeaderAuthorization: protocol.BearerPrefix + goldenEvalKey,
				protocol.HeaderContentType:   protocol.MIMEApplicationJSON,
			},
		},
		{
			name:    "allflags_admin_key",
			method:  http.MethodPost,
			path:    v1Allflags,
			body:    flagBody,
			headers: bearer(goldenAdminKey),
		},
		{
			name:    "eval_no_key",
			method:  http.MethodPost,
			path:    v1FeatureTest,
			body:    flagBody,
			headers: jsonHeaders(),
		},
		{
			name: "eval_ok_bearer", method: http.MethodPost, path: v1FeatureTest, body: flagBody,
			headers: bearer(goldenEvalKey),
		},
		{
			name:    "collector_no_key",
			method:  http.MethodPost,
			path:    v1DataCollector,
			body:    emptyEventsBody,
			headers: jsonHeaders(),
		},
		{name: "flag_change_no_key", method: http.MethodGet, path: v1FlagChange},
		{name: "flag_change_ok", method: http.MethodGet, path: v1FlagChange, headers: bearer(goldenEvalKey)},
		{name: "configuration_no_key", method: http.MethodPost, path: v1FlagConfiguration, headers: jsonHeaders()},
		{
			name:    "ofrep_bulk_no_key",
			method:  http.MethodPost,
			path:    fixtures.OFREPEvaluateFlagsPath,
			body:    contextTargetingKeyUserOne,
			headers: jsonHeaders(),
		},
		{
			name:    "ofrep_bulk_ok",
			method:  http.MethodPost,
			path:    fixtures.OFREPEvaluateFlagsPath,
			body:    contextTargetingKeyUserOne,
			headers: bearer(goldenEvalKey),
		},
		{
			name:       "ofrep_bulk_304",
			method:     http.MethodPost,
			path:       fixtures.OFREPEvaluateFlagsPath,
			body:       contextTargetingKeyUserOne,
			headers:    bearer(goldenEvalKey),
			followETag: true,
		},
		{
			name:    "ofrep_eval_no_key",
			method:  http.MethodPost,
			path:    ofrepEvaluateTestFlag,
			body:    contextTargetingKeyUserOne,
			headers: jsonHeaders(),
		},
		{
			name:    "ofrep_eval_ok",
			method:  http.MethodPost,
			path:    ofrepEvaluateTestFlag,
			body:    contextTargetingKeyUserOne,
			headers: bearer(goldenEvalKey),
		},
		{name: "manifest_no_key", method: http.MethodGet, path: openfeatureV0Manifest},
		{name: "manifest_ok", method: http.MethodGet, path: openfeatureV0Manifest, headers: bearer(goldenEvalKey)},
		{
			name:    "admin_refresh_eval_key",
			method:  http.MethodPost,
			path:    adminV1Retriever,
			headers: bearer(goldenEvalKey),
		},
		{
			name:    "admin_refresh_ok",
			method:  http.MethodPost,
			path:    adminV1Retriever,
			headers: bearer(goldenAdminKey),
		},
		{
			name: "admin_validate_ok", method: http.MethodPost, path: adminV1Flags,
			headers: bearer(goldenAdminKey), body: `{"f":{"variations":{"a":1,"b":2},"defaultRule":{"variation":"a"}}}`,
		},
		{
			name: "admin_validate_invalid_flag", method: http.MethodPost, path: adminV1Flags,
			headers: bearer(goldenAdminKey), body: `{"f":{"variations":{"a":1},"defaultRule":{"variation":"zzz"}}}`,
		},
		{
			name: "admin_validate_unparsable", method: http.MethodPost, path: adminV1Flags,
			headers: bearer(goldenAdminKey), body: `{`,
		},
		{
			name:   "admin_validate_yaml",
			method: http.MethodPost,
			path:   adminV1Flags,
			headers: map[string]string{
				protocol.HeaderAuthorization: protocol.BearerPrefix + goldenAdminKey,
				protocol.HeaderContentType:   "application/yaml",
			},
			body: "f:\n  variations:\n    a: 1\n    b: 2\n  defaultRule:\n    variation: a\n",
		},
		{
			name:   "admin_validate_toml",
			method: http.MethodPost,
			path:   adminV1Flags,
			headers: map[string]string{
				protocol.HeaderAuthorization: protocol.BearerPrefix + goldenAdminKey,
				protocol.HeaderContentType:   "application/toml",
			},
			body: "[f]\n[f.variations]\na = 1\nb = 2\n[f.defaultRule]\nvariation = \"a\"\n",
		},
		{
			name:   "admin_validate_wrong_ct",
			method: http.MethodPost,
			path:   adminV1Flags,
			headers: map[string]string{
				protocol.HeaderAuthorization: protocol.BearerPrefix + goldenAdminKey,
				protocol.HeaderContentType:   textPlain,
			},
			body: `{}`,
		},
		{
			name:   "admin_validate_no_ct",
			method: http.MethodPost,
			path:   adminV1Flags,
			headers: map[string]string{
				protocol.HeaderAuthorization: protocol.BearerPrefix + goldenAdminKey,
			},
			body: `{}`,
		},
		{
			name: "admin_validate_too_large", method: http.MethodPost, path: adminV1Flags,
			headers: bearer(goldenAdminKey), body: `{"f":"` + strings.Repeat("a", 2<<20) + `"}`,
		},
		{name: "ws_no_key", method: http.MethodGet, path: streamV1Ws},
		{name: "ws_bad_key", method: http.MethodGet, path: "/stream/v1/ws/flag/change?apiKey=zzz"},
		{
			name:   "ws_ok_key_no_upgrade",
			method: http.MethodGet,
			path:   "/stream/v1/ws/flag/change?apiKey=" + goldenEvalKey,
		},
		{name: "ws_legacy_no_key", method: http.MethodGet, path: fixtures.WebsocketFlagChangePath},
		{name: "sse_no_key", method: http.MethodGet, path: streamV1Sse},
		{
			name:        "sse_ok_key",
			method:      http.MethodGet,
			path:        "/stream/v1/sse/flag/change?apiKey=" + goldenEvalKey,
			headersOnly: true,
		},
		{name: "health_no_key", method: http.MethodGet, path: healthPath},
		{name: "info_no_key", method: http.MethodGet, path: info},
		{name: "metrics_no_key", method: http.MethodGet, path: metricsPath, noBody: true},
		{name: "notfound_no_key", method: http.MethodGet, path: nope},
		{name: "method_get_allflags_no_key", method: http.MethodGet, path: v1Allflags},
	}
}

// Test_Golden checks every route and every request that no route serves against the golden files, for each
// configuration of the relay proxy: the files in testdata/golden/<configuration>, the series of the HTTP metrics in
// testdata/golden/metrics.
func Test_Golden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		configure func(*config.Config)
		name      string
		cases     []goldenCase
		series    bool
	}{
		{
			name:      "open",
			configure: func(c *config.Config) { c.Swagger.Enabled = true },
			cases:     goldenOpenCases(),
		},
		{
			name: "auth",
			configure: func(c *config.Config) {
				c.AuthorizedKeys = config.APIKeys{Evaluation: []string{goldenEvalKey}, Admin: []string{goldenAdminKey}}
				c.ForceReloadAPIKeys()
			},
			cases: goldenAuthCases(),
		},
		{
			name:      "noversion",
			configure: func(c *config.Config) { c.DisableVersionHeader = true },
			cases: []goldenCase{
				{name: "health", method: http.MethodGet, path: healthPath},
				{name: "notfound", method: http.MethodGet, path: nope},
			},
		},
		{
			name:      "pprof",
			configure: func(c *config.Config) { c.EnablePprof = true },
			cases: []goldenCase{
				{name: "index", method: http.MethodGet, path: debugPprof},
				{name: "cmdline", method: http.MethodGet, path: "/debug/pprof/cmdline"},
				{name: "symbol", method: http.MethodGet, path: "/debug/pprof/symbol"},
				{name: "unknown_profile", method: http.MethodGet, path: "/debug/pprof/nope"},
				{name: "post_index", method: http.MethodPost, path: debugPprof},
			},
		},
		{name: metricsName, configure: func(*config.Config) {}, series: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conf := testConfig(t)
			conf.Version = goldenVersion
			tt.configure(conf)

			if tt.series {
				checkMetricSeries(t, startGoldenServer(t, conf))

				return
			}

			runGoldenCases(t, conf, tt.name, tt.cases)
		})
	}
}

// runGoldenCases starts a relay proxy and checks the response to every case against its golden file.
func runGoldenCases(t *testing.T, conf *config.Config, variant string, cases []goldenCase) {
	t.Helper()
	baseURL := startGoldenServer(t, conf)
	t.Run("cases", func(t *testing.T) {
		t.Parallel()

		for _, tt := range cases {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got := recordGolden(t, baseURL, &tt)

				path := filepath.Join(goldenDir, variant, tt.name+".golden")
				if os.Getenv(goldenUpdate) != "" {
					require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
					require.NoError(t, os.WriteFile(path, []byte(got), 0o600))

					return
				}

				want, err := os.ReadFile(path)
				require.NoError(t, err, "missing golden file, run with GOLDEN_UPDATE=1")
				assert.Equal(t, string(want), got)
			})
		}
	})
}

// startGoldenServer is startTestServer with the streaming services, so that the stream routes serve.
func startGoldenServer(t *testing.T, conf *config.Config, opts ...api.Option) string {
	t.Helper()

	return startWrappedServer(t, conf, func(ln net.Listener) net.Listener { return ln }, opts...)
}

// startWrappedServer is startGoldenServer with the listener of the server wrapped by wrap: a test can watch what the
// server does to its connections.
func startWrappedServer(
	t *testing.T, conf *config.Config, wrap func(net.Listener) net.Listener, opts ...api.Option,
) string {
	t.Helper()
	listener, port := testhelpers.ListenLocal(t)
	listener = wrap(listener)
	conf.Server.Port = port
	log := newTestLogger(t)

	metrics, err := metric.NewMetrics()
	require.NoError(t, err)

	wsService := stream.NewWebsocketService()
	sseService := stream.NewSSEService()
	flagsetManager, err := service.NewFlagsetManager(conf, log.Slog, nil, nil, nil)
	require.NoError(t, err)

	srv := newTestServer(t, conf, &service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  wsService,
		SSEService:        sseService,
		FlagsetManager:    flagsetManager,
		Metrics:           metrics,
	}, log.Slog, append([]api.Option{api.WithListener(listener)}, opts...)...)
	go func() { srv.StartWithContext(context.Background()) }()

	t.Cleanup(func() { srv.Stop(context.Background()) })
	// cleanups run last in, first out: the streams end first, so the graceful shutdown has nothing to wait for.
	t.Cleanup(wsService.Close)
	t.Cleanup(sseService.Close)

	baseURL := fmt.Sprintf(httpLocalhostD, port)
	waitForServer(t, baseURL)

	return baseURL
}

// goldenHeaders are the response headers that are part of the contract.
func goldenHeaders() []string {
	return []string{
		"Access-Control-Allow-Headers",
		"Access-Control-Allow-Methods",
		accessControlAllowOrigin,
		"Allow",
		"Cache-Control",
		protocol.HeaderContentType,
		"Deprecation",
		"Etag",
		"Last-Modified",
		"Link",
		"Location",
		vary,
		xGofeatureflagVersion,
		"X-Manifest-Capabilities",
		"X-Semaphore-Version",
		"X-Content-Type-Options",
	}
}

func recordGolden(t *testing.T, baseURL string, tt *goldenCase) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	doReq := func(extra map[string]string) *http.Response {
		var body io.Reader
		if tt.body != "" {
			body = strings.NewReader(tt.body)
		}

		req, err := http.NewRequestWithContext(ctx, tt.method, baseURL+tt.path, body)
		require.NoError(t, err)

		for k, v := range tt.headers {
			req.Header.Set(k, v)
		}

		for k, v := range extra {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		require.NoError(t, err)

		return resp
	}

	resp := doReq(nil)
	if tt.followETag {
		etag := resp.Header.Get("ETag")
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		require.NotEmpty(t, etag, "the first response has no ETag")
		resp = doReq(map[string]string{protocol.HeaderIfNoneMatch: etag})
	}
	defer func() { _ = resp.Body.Close() }()

	var buf strings.Builder
	fmt.Fprintf(&buf, "status: %d\n", resp.StatusCode)

	for _, h := range goldenHeaders() {
		if vals := resp.Header.Values(h); len(vals) > 0 {
			fmt.Fprintf(&buf, "%s: %s\n", h, strings.Join(vals, " | "))
		}
	}

	buf.WriteString("---\n")

	switch {
	case tt.headersOnly:
		buf.WriteString("(stream, body not recorded)\n")
	case tt.noBody:
		_, _ = io.Copy(io.Discard, resp.Body)

		buf.WriteString(bodyNotRecordedN)
	case tt.name == "swagger_index" || strings.HasPrefix(tt.name, "swagger_"):
		// the document changes with the swagger library, status and content type are what is pinned.
		_, _ = io.Copy(io.Discard, resp.Body)

		buf.WriteString(bodyNotRecordedN)
	case strings.HasPrefix(tt.path, debugPprof) && resp.StatusCode == http.StatusOK:
		_, _ = io.Copy(io.Discard, resp.Body)

		buf.WriteString(bodyNotRecordedN)
	default:
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		fmt.Fprintf(&buf, "%q\n", string(b))
	}

	return buf.String()
}

// checkMetricSeries pins the series of the HTTP metrics after a fixed sequence of requests: the names, the labels
// and the value of the url label (the route, not the path) are what the dashboards depend on.
func checkMetricSeries(t *testing.T, baseURL string) {
	t.Helper()

	requests := []goldenCase{
		{method: http.MethodGet, path: healthPath},
		{method: http.MethodPost, path: v1Allflags, body: flagBody, headers: jsonHeaders()},
		{method: http.MethodPost, path: v1FeatureTest, body: flagBody, headers: jsonHeaders()},
		{method: http.MethodPost, path: v1FeatureTest, body: flagBody, headers: jsonHeaders()},
		{
			method:  http.MethodPost,
			path:    ofrepEvaluateTestFlag,
			body:    contextTargetingKeyUserOne,
			headers: jsonHeaders(),
		},
		{method: http.MethodPost, path: ofrepEvaluateNope, body: contextTargetingKeyUserOne, headers: jsonHeaders()},
		{method: http.MethodGet, path: nope},
		{method: http.MethodGet, path: v1Allflags},
		{method: http.MethodDelete, path: healthPath},
		{method: http.MethodGet, path: v1FlagChange},
	}
	for _, r := range requests {
		_ = recordGolden(t, baseURL, &r)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+metricsPath, http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	var lines []string

	scanner := bufio.NewScanner(resp.Body)
	hostLabel := regexp.MustCompile(`host="[^"]*"`)

	for scanner.Scan() {
		line := scanner.Text()

		keep := strings.HasPrefix(line, "# TYPE semaphore_") ||
			strings.HasPrefix(line, "semaphore_requests_total{") ||
			(strings.HasPrefix(line, "semaphore_request_duration_seconds_count{")) ||
			(strings.HasPrefix(line, "semaphore_request_size_bytes_count{")) ||
			(strings.HasPrefix(line, "semaphore_response_size_bytes_count{"))
		if keep {
			lines = append(lines, hostLabel.ReplaceAllString(line, `host="HOST"`))
		}
	}

	require.NoError(t, scanner.Err())
	slices.Sort(lines)

	got := strings.Join(lines, "\n") + "\n"

	path := filepath.Join(goldenDir, metricsName, "http_series.golden")
	if os.Getenv(goldenUpdate) != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))

		return
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}
