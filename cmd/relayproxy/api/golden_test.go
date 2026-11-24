package api_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	"github.com/blackbirdworks/semaphore/testutils"
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
	ofrepBody      = `{"context":{"targetingKey":"user-1"}}`
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

func jsonHeaders() map[string]string { return map[string]string{"Content-Type": "application/json"} }

func withHeaders(extra map[string]string) map[string]string {
	h := jsonHeaders()
	maps.Copy(h, extra)

	return h
}

// goldenOpenCases are served by a relay proxy that has no API keys.
func goldenOpenCases() []goldenCase {
	return []goldenCase{
		// the 17 routes, success
		{name: "allflags_ok", method: http.MethodPost, path: "/v1/allflags", body: flagBody, headers: jsonHeaders()},
		{
			name: "allflags_no_etag", method: http.MethodPost, path: "/v1/allflags", body: flagBody,
			headers: withHeaders(map[string]string{"If-None-Match": `"x"`}),
		},
		{
			name: "eval_ok", method: http.MethodPost, path: "/v1/feature/test-flag/eval", body: flagBody,
			headers: jsonHeaders(),
		},
		{
			name: "eval_unknown_flag", method: http.MethodPost, path: "/v1/feature/nope/eval", body: flagBody,
			headers: jsonHeaders(),
		},
		{
			name:   "collector_ok",
			method: http.MethodPost,
			path:   "/v1/data/collector",
			body: `{"meta":{"app":"x"},"events":[{"kind":"feature","contextKind":"user","userKey":"u",` +
				`"creationDate":1,"key":"test-flag","variation":"True","value":true,"default":false}]}`,
			headers: jsonHeaders(),
		},
		{name: "flag_change_ok", method: http.MethodGet, path: "/v1/flag/change"},
		{name: "flag_change_304", method: http.MethodGet, path: "/v1/flag/change", followETag: true},
		{
			name:    "configuration_ok",
			method:  http.MethodPost,
			path:    "/v1/flag/configuration",
			body:    `{"flags":["test-flag"]}`,
			headers: jsonHeaders(),
		},
		{
			name: "configuration_all", method: http.MethodPost, path: "/v1/flag/configuration", headers: jsonHeaders(),
		},
		{
			name: "configuration_304", method: http.MethodPost, path: "/v1/flag/configuration",
			headers: jsonHeaders(), followETag: true,
		},
		{name: "swagger_index", method: http.MethodGet, path: "/swagger/index.html", headers: nil},
		{name: "swagger_dir_slash", method: http.MethodGet, path: "/swagger/"},
		{name: "swagger_openapi", method: http.MethodGet, path: "/swagger/openapi.json", headers: nil},
		{
			name: "ofrep_bulk_ok", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags", body: ofrepBody,
			headers: jsonHeaders(),
		},
		{
			name: "ofrep_bulk_304", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags", body: ofrepBody,
			headers: jsonHeaders(), followETag: true,
		},
		{
			name: "ofrep_eval_ok", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags/test-flag", body: ofrepBody,
			headers: jsonHeaders(),
		},
		{
			name: "ws_no_upgrade", method: http.MethodGet, path: "/stream/v1/ws/flag/change",
		},
		{
			name: "sse_ok", method: http.MethodGet, path: "/stream/v1/sse/flag/change", headersOnly: true,
		},
		{name: "ws_legacy_no_upgrade", method: http.MethodGet, path: "/ws/v1/flag/change"},
		{name: "metrics", method: http.MethodGet, path: "/metrics", noBody: true},
		{name: "health", method: http.MethodGet, path: "/health"},
		{name: "info", method: http.MethodGet, path: "/info"},
		{name: "manifest_ok", method: http.MethodGet, path: "/openfeature/v0/manifest"},

		// admin routes (no key configured: the admin routes refuse everybody)
		{name: "admin_refresh_noauth", method: http.MethodPost, path: "/admin/v1/retriever/refresh"},
		{
			name: "admin_validate_noauth", method: http.MethodPost, path: "/admin/v1/flags/validate",
			headers: jsonHeaders(), body: `{}`,
		},

		// error bodies of the handlers
		{
			name: "allflags_bad_json", method: http.MethodPost, path: "/v1/allflags", body: `{"evaluationContext":`,
			headers: jsonHeaders(),
		},
		{
			name:    "allflags_wrong_type",
			method:  http.MethodPost,
			path:    "/v1/allflags",
			body:    `{"evaluationContext":42}`,
			headers: jsonHeaders(),
		},
		{
			name: "allflags_no_context", method: http.MethodPost, path: "/v1/allflags", body: `{}`,
			headers: jsonHeaders(),
		},
		{
			name: "allflags_empty_body", method: http.MethodPost, path: "/v1/allflags", headers: jsonHeaders(),
		},
		{
			name: "allflags_legacy_user", method: http.MethodPost, path: "/v1/allflags",
			body: `{"user":{"key":"u"}}`, headers: jsonHeaders(),
		},
		{
			name: "allflags_text_plain", method: http.MethodPost, path: "/v1/allflags", body: flagBody,
			headers: map[string]string{"Content-Type": "text/plain"},
		},
		{
			name: "allflags_charset", method: http.MethodPost, path: "/v1/allflags", body: flagBody,
			headers: map[string]string{"Content-Type": "application/json; charset=utf-8"},
		},
		{
			name: "allflags_accept_html", method: http.MethodPost, path: "/v1/allflags", body: flagBody,
			headers: withHeaders(map[string]string{"Accept": "text/html"}),
		},
		{
			name: "allflags_unknown_field", method: http.MethodPost, path: "/v1/allflags",
			body: `{"evaluationContext":{"key":"u"},"extra":1}`, headers: jsonHeaders(),
		},
		{
			name: "eval_bad_json", method: http.MethodPost, path: "/v1/feature/test-flag/eval", body: `nope`,
			headers: jsonHeaders(),
		},
		{
			name: "eval_no_context", method: http.MethodPost, path: "/v1/feature/test-flag/eval", body: `{}`,
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
			name: "collector_bad_json", method: http.MethodPost, path: "/v1/data/collector", body: `[`,
			headers: jsonHeaders(),
		},
		{
			name: "collector_no_events", method: http.MethodPost, path: "/v1/data/collector", body: `{}`,
			headers: jsonHeaders(),
		},
		{
			name: "configuration_bad_json", method: http.MethodPost, path: "/v1/flag/configuration", body: `{`,
			headers: jsonHeaders(),
		},
		{
			name: "ofrep_bulk_bad_json", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags", body: `{`,
			headers: jsonHeaders(),
		},
		{
			name: "ofrep_bulk_no_context", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags", body: `{}`,
			headers: jsonHeaders(),
		},
		{
			name:    "ofrep_bulk_empty_body",
			method:  http.MethodPost,
			path:    "/ofrep/v1/evaluate/flags",
			headers: jsonHeaders(),
		},
		{
			name: "ofrep_bulk_text_plain", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags", body: ofrepBody,
			headers: map[string]string{"Content-Type": "text/plain"},
		},
		{
			name: "ofrep_bulk_flag_list", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags",
			body: `{"context":{"targetingKey":"u","flags":["test-flag"]}}`, headers: jsonHeaders(),
		},
		{
			name: "ofrep_eval_empty_key", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags/", body: ofrepBody,
			headers: jsonHeaders(),
		},
		{
			name:    "ofrep_eval_not_found",
			method:  http.MethodPost,
			path:    "/ofrep/v1/evaluate/flags/nope",
			body:    ofrepBody,
			headers: jsonHeaders(),
		},
		{
			name: "ofrep_eval_bad_json", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags/test-flag", body: `{`,
			headers: jsonHeaders(),
		},
		{
			name:    "ofrep_eval_no_context",
			method:  http.MethodPost,
			path:    "/ofrep/v1/evaluate/flags/test-flag",
			body:    `{}`,
			headers: jsonHeaders(),
		},
		{
			name: "ofrep_eval_escaped_key", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags/test%2Dflag",
			body: ofrepBody, headers: jsonHeaders(),
		},
		{
			name: "ofrep_eval_no_etag", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags/test-flag",
			body: ofrepBody, headers: withHeaders(map[string]string{"If-None-Match": `"x"`}),
		},
		{
			name:        "ws_bad_apikey_flagset",
			method:      http.MethodGet,
			path:        "/stream/v1/sse/flag/change?apiKey=zzz",
			headersOnly: true,
		},

		// 404, 405, preflight and the other methods
		{name: "notfound_root", method: http.MethodGet, path: "/"},
		{name: "notfound_unknown", method: http.MethodGet, path: "/nope"},
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
		{name: "method_get_allflags", method: http.MethodGet, path: "/v1/allflags"},
		{
			name:    "method_put_allflags",
			method:  http.MethodPut,
			path:    "/v1/allflags",
			body:    flagBody,
			headers: jsonHeaders(),
		},
		{name: "method_delete_health", method: http.MethodDelete, path: "/health"},
		{
			name:    "method_post_flag_change",
			method:  http.MethodPost,
			path:    "/v1/flag/change",
			body:    `{}`,
			headers: jsonHeaders(),
		},
		{name: "method_get_ofrep_bulk", method: http.MethodGet, path: "/ofrep/v1/evaluate/flags"},
		{name: "method_get_ofrep_eval", method: http.MethodGet, path: "/ofrep/v1/evaluate/flags/test-flag"},
		{name: "method_get_admin_refresh", method: http.MethodGet, path: "/admin/v1/retriever/refresh"},
		{name: "method_post_swagger", method: http.MethodPost, path: "/swagger/index.html"},
		{name: "method_post_manifest", method: http.MethodPost, path: "/openfeature/v0/manifest"},
		{name: "method_post_ws", method: http.MethodPost, path: "/stream/v1/ws/flag/change"},
		{name: "head_health", method: http.MethodHead, path: "/health"},
		{name: "head_flag_change", method: http.MethodHead, path: "/v1/flag/change"},
		{name: "head_allflags", method: http.MethodHead, path: "/v1/allflags"},
		{name: "head_notfound", method: http.MethodHead, path: "/nope"},
		{name: "options_no_origin", method: http.MethodOptions, path: "/v1/allflags"},
		{
			name: "options_preflight", method: http.MethodOptions, path: "/v1/allflags",
			headers: map[string]string{
				"Origin":                         "https://example.com",
				"Access-Control-Request-Method":  "POST",
				"Access-Control-Request-Headers": "content-type,authorization",
			},
		},
		{
			name: "options_preflight_unknown_path", method: http.MethodOptions, path: "/nope",
			headers: map[string]string{
				"Origin": "https://example.com", "Access-Control-Request-Method": "GET",
			},
		},
		{
			name: "cors_simple", method: http.MethodGet, path: "/health",
			headers: map[string]string{"Origin": "https://example.com"},
		},
		{name: "options_health_no_origin", method: http.MethodOptions, path: "/health"},
		{
			name: "options_health_preflight", method: http.MethodOptions, path: "/health",
			headers: map[string]string{
				"Origin": "https://example.com", "Access-Control-Request-Method": "GET",
			},
		},
		{
			name: "options_health_origin_only", method: http.MethodOptions, path: "/health",
			headers: map[string]string{"Origin": "https://example.com"},
		},
		{
			name: "options_swagger_preflight", method: http.MethodOptions, path: "/swagger/index.html",
			headers: map[string]string{
				"Origin": "https://example.com", "Access-Control-Request-Method": "GET",
				"Access-Control-Request-Headers": "x-api-key",
			},
		},
		{name: "head_info", method: http.MethodHead, path: "/info"},
		{name: "head_manifest", method: http.MethodHead, path: "/openfeature/v0/manifest"},
		{name: "info_trailing_slash", method: http.MethodGet, path: "/info/"},
		{name: "metrics_post", method: http.MethodPost, path: "/metrics"},
		{
			name: "cors_simple_error", method: http.MethodGet, path: "/nope",
			headers: map[string]string{"Origin": "https://example.com"},
		},
		{name: "trace_method", method: http.MethodTrace, path: "/health"},
		{name: "connect_method", method: http.MethodConnect, path: "/health"},
		{name: "patch_allflags", method: http.MethodPatch, path: "/v1/allflags", body: `{}`, headers: jsonHeaders()},
		{name: "pprof_disabled", method: http.MethodGet, path: "/debug/pprof/"},
	}
}

// goldenAuthCases are served by a relay proxy with an evaluation key and an admin key.
func goldenAuthCases() []goldenCase {
	bearer := func(k string) map[string]string {
		return map[string]string{"Authorization": "Bearer " + k, "Content-Type": "application/json"}
	}
	apiKey := func(k string) map[string]string {
		return map[string]string{"X-API-Key": k, "Content-Type": "application/json"}
	}

	return []goldenCase{
		{
			name:    "allflags_no_key",
			method:  http.MethodPost,
			path:    "/v1/allflags",
			body:    flagBody,
			headers: jsonHeaders(),
		},
		{
			name:    "allflags_bearer_ok",
			method:  http.MethodPost,
			path:    "/v1/allflags",
			body:    flagBody,
			headers: bearer(goldenEvalKey),
		},
		{
			name: "allflags_bearer_lower", method: http.MethodPost, path: "/v1/allflags", body: flagBody,
			headers: map[string]string{"Authorization": "bearer " + goldenEvalKey, "Content-Type": "application/json"},
		},
		{
			name:    "allflags_bearer_bad",
			method:  http.MethodPost,
			path:    "/v1/allflags",
			body:    flagBody,
			headers: bearer("zzz"),
		},
		{
			name: "allflags_basic_scheme", method: http.MethodPost, path: "/v1/allflags", body: flagBody,
			headers: map[string]string{"Authorization": "Basic abc", "Content-Type": "application/json"},
		},
		{
			name: "allflags_raw_authorization", method: http.MethodPost, path: "/v1/allflags", body: flagBody,
			headers: map[string]string{"Authorization": goldenEvalKey, "Content-Type": "application/json"},
		},
		{
			name: "allflags_bearer_empty", method: http.MethodPost, path: "/v1/allflags", body: flagBody,
			headers: map[string]string{"Authorization": "Bearer ", "Content-Type": "application/json"},
		},
		{
			name:    "allflags_xapikey_ok",
			method:  http.MethodPost,
			path:    "/v1/allflags",
			body:    flagBody,
			headers: apiKey(goldenEvalKey),
		},
		{
			name:    "allflags_xapikey_bad",
			method:  http.MethodPost,
			path:    "/v1/allflags",
			body:    flagBody,
			headers: apiKey("zzz"),
		},
		{
			name: "allflags_xapikey_bad_bearer_ok", method: http.MethodPost, path: "/v1/allflags", body: flagBody,
			headers: map[string]string{
				"X-API-Key": "zzz", "Authorization": "Bearer " + goldenEvalKey, "Content-Type": "application/json",
			},
		},
		{
			name:    "allflags_admin_key",
			method:  http.MethodPost,
			path:    "/v1/allflags",
			body:    flagBody,
			headers: bearer(goldenAdminKey),
		},
		{
			name:    "eval_no_key",
			method:  http.MethodPost,
			path:    "/v1/feature/test-flag/eval",
			body:    flagBody,
			headers: jsonHeaders(),
		},
		{
			name: "eval_ok_bearer", method: http.MethodPost, path: "/v1/feature/test-flag/eval", body: flagBody,
			headers: bearer(goldenEvalKey),
		},
		{
			name:    "collector_no_key",
			method:  http.MethodPost,
			path:    "/v1/data/collector",
			body:    `{"events":[]}`,
			headers: jsonHeaders(),
		},
		{name: "flag_change_no_key", method: http.MethodGet, path: "/v1/flag/change"},
		{name: "flag_change_ok", method: http.MethodGet, path: "/v1/flag/change", headers: bearer(goldenEvalKey)},
		{name: "configuration_no_key", method: http.MethodPost, path: "/v1/flag/configuration", headers: jsonHeaders()},
		{
			name:    "ofrep_bulk_no_key",
			method:  http.MethodPost,
			path:    "/ofrep/v1/evaluate/flags",
			body:    ofrepBody,
			headers: jsonHeaders(),
		},
		{
			name: "ofrep_bulk_ok", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags", body: ofrepBody,
			headers: bearer(goldenEvalKey),
		},
		{
			name: "ofrep_bulk_304", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags", body: ofrepBody,
			headers: bearer(goldenEvalKey), followETag: true,
		},
		{
			name:    "ofrep_eval_no_key",
			method:  http.MethodPost,
			path:    "/ofrep/v1/evaluate/flags/test-flag",
			body:    ofrepBody,
			headers: jsonHeaders(),
		},
		{
			name: "ofrep_eval_ok", method: http.MethodPost, path: "/ofrep/v1/evaluate/flags/test-flag", body: ofrepBody,
			headers: bearer(goldenEvalKey),
		},
		{name: "manifest_no_key", method: http.MethodGet, path: "/openfeature/v0/manifest"},
		{name: "manifest_ok", method: http.MethodGet, path: "/openfeature/v0/manifest", headers: bearer(goldenEvalKey)},
		{
			name:    "admin_refresh_eval_key",
			method:  http.MethodPost,
			path:    "/admin/v1/retriever/refresh",
			headers: bearer(goldenEvalKey),
		},
		{
			name:    "admin_refresh_ok",
			method:  http.MethodPost,
			path:    "/admin/v1/retriever/refresh",
			headers: bearer(goldenAdminKey),
		},
		{
			name: "admin_validate_ok", method: http.MethodPost, path: "/admin/v1/flags/validate",
			headers: bearer(goldenAdminKey), body: `{"f":{"variations":{"a":1,"b":2},"defaultRule":{"variation":"a"}}}`,
		},
		{
			name: "admin_validate_invalid_flag", method: http.MethodPost, path: "/admin/v1/flags/validate",
			headers: bearer(goldenAdminKey), body: `{"f":{"variations":{"a":1},"defaultRule":{"variation":"zzz"}}}`,
		},
		{
			name: "admin_validate_unparsable", method: http.MethodPost, path: "/admin/v1/flags/validate",
			headers: bearer(goldenAdminKey), body: `{`,
		},
		{
			name: "admin_validate_yaml", method: http.MethodPost, path: "/admin/v1/flags/validate",
			headers: map[string]string{"Authorization": "Bearer " + goldenAdminKey, "Content-Type": "application/yaml"},
			body:    "f:\n  variations:\n    a: 1\n    b: 2\n  defaultRule:\n    variation: a\n",
		},
		{
			name: "admin_validate_toml", method: http.MethodPost, path: "/admin/v1/flags/validate",
			headers: map[string]string{"Authorization": "Bearer " + goldenAdminKey, "Content-Type": "application/toml"},
			body:    "[f]\n[f.variations]\na = 1\nb = 2\n[f.defaultRule]\nvariation = \"a\"\n",
		},
		{
			name:   "admin_validate_wrong_ct",
			method: http.MethodPost,
			path:   "/admin/v1/flags/validate",
			headers: map[string]string{
				"Authorization": "Bearer " + goldenAdminKey,
				"Content-Type":  "text/plain",
			},
			body: `{}`,
		},
		{
			name: "admin_validate_no_ct", method: http.MethodPost, path: "/admin/v1/flags/validate",
			headers: map[string]string{"Authorization": "Bearer " + goldenAdminKey}, body: `{}`,
		},
		{
			name: "admin_validate_too_large", method: http.MethodPost, path: "/admin/v1/flags/validate",
			headers: bearer(goldenAdminKey), body: `{"f":"` + strings.Repeat("a", 2<<20) + `"}`,
		},
		{name: "ws_no_key", method: http.MethodGet, path: "/stream/v1/ws/flag/change"},
		{name: "ws_bad_key", method: http.MethodGet, path: "/stream/v1/ws/flag/change?apiKey=zzz"},
		{
			name:   "ws_ok_key_no_upgrade",
			method: http.MethodGet,
			path:   "/stream/v1/ws/flag/change?apiKey=" + goldenEvalKey,
		},
		{name: "ws_legacy_no_key", method: http.MethodGet, path: "/ws/v1/flag/change"},
		{name: "sse_no_key", method: http.MethodGet, path: "/stream/v1/sse/flag/change"},
		{
			name:        "sse_ok_key",
			method:      http.MethodGet,
			path:        "/stream/v1/sse/flag/change?apiKey=" + goldenEvalKey,
			headersOnly: true,
		},
		{name: "health_no_key", method: http.MethodGet, path: "/health"},
		{name: "info_no_key", method: http.MethodGet, path: "/info"},
		{name: "metrics_no_key", method: http.MethodGet, path: "/metrics", noBody: true},
		{name: "notfound_no_key", method: http.MethodGet, path: "/nope"},
		{name: "method_get_allflags_no_key", method: http.MethodGet, path: "/v1/allflags"},
	}
}

func Test_Golden_Open(t *testing.T) {
	t.Parallel()
	conf := testConfig(t)
	conf.Version = goldenVersion
	conf.Swagger.Enabled = true
	runGoldenCases(t, conf, "open", goldenOpenCases())
}

func Test_Golden_Auth(t *testing.T) {
	t.Parallel()
	conf := testConfig(t)
	conf.Version = goldenVersion
	conf.AuthorizedKeys = config.APIKeys{Evaluation: []string{goldenEvalKey}, Admin: []string{goldenAdminKey}}
	conf.ForceReloadAPIKeys()
	runGoldenCases(t, conf, "auth", goldenAuthCases())
}

func Test_Golden_NoVersionHeader(t *testing.T) {
	t.Parallel()
	conf := testConfig(t)
	conf.Version = goldenVersion
	conf.DisableVersionHeader = true
	runGoldenCases(t, conf, "noversion", []goldenCase{
		{name: "health", method: http.MethodGet, path: "/health"},
		{name: "notfound", method: http.MethodGet, path: "/nope"},
	})
}

func Test_Golden_Pprof(t *testing.T) {
	t.Parallel()
	conf := testConfig(t)
	conf.Version = goldenVersion
	conf.EnablePprof = true
	runGoldenCases(t, conf, "pprof", []goldenCase{
		{name: "index", method: http.MethodGet, path: "/debug/pprof/"},
		{name: "cmdline", method: http.MethodGet, path: "/debug/pprof/cmdline"},
		{name: "symbol", method: http.MethodGet, path: "/debug/pprof/symbol"},
		{name: "unknown_profile", method: http.MethodGet, path: "/debug/pprof/nope"},
		{name: "post_index", method: http.MethodPost, path: "/debug/pprof/"},
	})
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
				got := recordGolden(t, baseURL, tt)
				path := filepath.Join(goldenDir, variant, tt.name+".golden")
				if os.Getenv("GOLDEN_UPDATE") != "" {
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
	ln, port := testutils.ListenLocal(t)
	conf.Server.Port = port
	log := newTestLogger(t)

	metrics, err := metric.NewMetrics()
	require.NoError(t, err)
	wsService := stream.NewWebsocketService()
	sseService := stream.NewSSEService()
	flagsetManager, err := service.NewFlagsetManager(conf, log.Slog, nil, nil, nil)
	require.NoError(t, err)

	s := newTestServer(t, conf, service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  wsService,
		SSEService:        sseService,
		FlagsetManager:    flagsetManager,
		Metrics:           metrics,
	}, log.Slog, append([]api.Option{api.WithListener(ln)}, opts...)...)
	go func() { s.StartWithContext(context.Background()) }()
	t.Cleanup(func() { s.Stop(context.Background()) })
	// cleanups run last in, first out: the streams end first, so the graceful shutdown has nothing to wait for.
	t.Cleanup(wsService.Close)
	t.Cleanup(sseService.Close)

	baseURL := fmt.Sprintf("http://localhost:%d", port)
	waitForServer(t, baseURL)

	return baseURL
}

// goldenHeaders are the response headers that are part of the contract.
func goldenHeaders() []string {
	return []string{
		"Access-Control-Allow-Headers", "Access-Control-Allow-Methods", "Access-Control-Allow-Origin",
		"Allow", "Cache-Control", "Content-Type", "Deprecation", "Etag", "Last-Modified", "Link", "Location",
		"Vary", "X-Gofeatureflag-Version", "X-Manifest-Capabilities", "X-Semaphore-Version",
		"X-Content-Type-Options",
	}
}

func recordGolden(t *testing.T, baseURL string, tt goldenCase) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	do := func(extra map[string]string) *http.Response {
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

	resp := do(nil)
	if tt.followETag {
		etag := resp.Header.Get("ETag")
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		require.NotEmpty(t, etag, "the first response has no ETag")
		resp = do(map[string]string{"If-None-Match": etag})
	}
	defer func() { _ = resp.Body.Close() }()

	var sb strings.Builder
	fmt.Fprintf(&sb, "status: %d\n", resp.StatusCode)
	for _, h := range goldenHeaders() {
		if vals := resp.Header.Values(h); len(vals) > 0 {
			fmt.Fprintf(&sb, "%s: %s\n", h, strings.Join(vals, " | "))
		}
	}
	sb.WriteString("---\n")
	switch {
	case tt.headersOnly:
		sb.WriteString("(stream, body not recorded)\n")
	case tt.noBody:
		_, _ = io.Copy(io.Discard, resp.Body)
		sb.WriteString("(body not recorded)\n")
	case tt.name == "swagger_index" || strings.HasPrefix(tt.name, "swagger_"):
		// the document changes with the swagger library, status and content type are what is pinned.
		_, _ = io.Copy(io.Discard, resp.Body)
		sb.WriteString("(body not recorded)\n")
	case strings.HasPrefix(tt.path, "/debug/pprof/") && resp.StatusCode == http.StatusOK:
		_, _ = io.Copy(io.Discard, resp.Body)
		sb.WriteString("(body not recorded)\n")
	default:
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		fmt.Fprintf(&sb, "%q\n", string(b))
	}

	return sb.String()
}

// Test_Golden_HTTPMetrics pins the series of the HTTP metrics after a fixed sequence of requests: the names, the
// labels and the value of the url label (the route, not the path) are what the dashboards depend on.
func Test_Golden_HTTPMetrics(t *testing.T) {
	t.Parallel()
	conf := testConfig(t)
	conf.Version = goldenVersion
	baseURL := startGoldenServer(t, conf)

	requests := []goldenCase{
		{method: http.MethodGet, path: "/health"},
		{method: http.MethodPost, path: "/v1/allflags", body: flagBody, headers: jsonHeaders()},
		{method: http.MethodPost, path: "/v1/feature/test-flag/eval", body: flagBody, headers: jsonHeaders()},
		{method: http.MethodPost, path: "/v1/feature/test-flag/eval", body: flagBody, headers: jsonHeaders()},
		{method: http.MethodPost, path: "/ofrep/v1/evaluate/flags/test-flag", body: ofrepBody, headers: jsonHeaders()},
		{method: http.MethodPost, path: "/ofrep/v1/evaluate/flags/nope", body: ofrepBody, headers: jsonHeaders()},
		{method: http.MethodGet, path: "/nope"},
		{method: http.MethodGet, path: "/v1/allflags"},
		{method: http.MethodDelete, path: "/health"},
		{method: http.MethodGet, path: "/v1/flag/change"},
	}
	for _, r := range requests {
		_ = recordGolden(t, baseURL, r)
	}

	resp, err := http.Get(baseURL + "/metrics")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var lines []string
	sc := bufio.NewScanner(resp.Body)
	hostLabel := regexp.MustCompile(`host="[^"]*"`)
	for sc.Scan() {
		line := sc.Text()
		keep := strings.HasPrefix(line, "# TYPE semaphore_") ||
			strings.HasPrefix(line, "semaphore_requests_total{") ||
			(strings.HasPrefix(line, "semaphore_request_duration_seconds_count{")) ||
			(strings.HasPrefix(line, "semaphore_request_size_bytes_count{")) ||
			(strings.HasPrefix(line, "semaphore_response_size_bytes_count{"))
		if keep {
			lines = append(lines, hostLabel.ReplaceAllString(line, `host="HOST"`))
		}
	}
	require.NoError(t, sc.Err())
	sort.Strings(lines)

	got := strings.Join(lines, "\n") + "\n"
	path := filepath.Join(goldenDir, "metrics", "http_series.golden")
	if os.Getenv("GOLDEN_UPDATE") != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))

		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}
