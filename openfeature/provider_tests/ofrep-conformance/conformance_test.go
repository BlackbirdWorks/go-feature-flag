package ofrepconformance_test

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ofrepconformance "github.com/blackbirdworks/semaphore/openfeature/provider_tests/ofrep-conformance"
)

const (
	httpClientTimeout = 5 * time.Second
	validToken        = "authorized_token"

	headerXAPIKey      = "X-API-Key"
	headerAuth         = "Authorization"
	headerIfNoneMatch  = "If-None-Match"
	jsonKeyField       = "key"
	jsonValueField     = "value"
	jsonReasonField    = "reason"
	jsonVariantField   = "variant"
	jsonErrorCodeField = "errorCode"
	reasonTargetMatch  = "TARGETING_MATCH"
	variantTrue        = "True"

	flagBoolMatch  = "bool_targeting_match"
	flagEnrichment = "flag-use-evaluation-context-enrichment"
	flagSemver     = "boolean_semver_targeting_match"

	defaultCtxJSON    = `{"context":{"targetingKey":"user-1","email":"john.doe@gofeatureflag.org","version":"10.0.0-10"}}`
	activeFlagListCtx = `{"context":{"targetingKey":"user-1","email":"john.doe@gofeatureflag.org",` +
		`"gofeatureflag":{"flagList":["bool_targeting_match","string_key","integer_key","double_key","object_key"]}}}`
)

func postOFREPRequest(
	t *testing.T,
	url string,
	headers map[string]string,
	body string,
) (int, http.Header, []byte) {
	t.Helper()

	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, reader)
	require.NoError(t, err)

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	for key, val := range headers {
		req.Header.Set(key, val)
	}

	client := &http.Client{Timeout: httpClientTimeout}

	resp, err := client.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, resp.Header, respBody
}

func TestOFREPConformance_SpecHeaders(t *testing.T) {
	t.Parallel()

	spec, err := ofrepconformance.LoadSpec(filepath.Join("testdata", "openapi.yaml"))
	require.NoError(t, err)

	assert.True(t, spec.HasHeader(bulkFlagsPath, http.MethodPost, http.StatusOK, "ETag"))
	assert.True(t, spec.HasHeader(singleFlagPath, http.MethodPost, http.StatusTooManyRequests, headerRetryAfter))
	assert.True(t, spec.HasHeader(bulkFlagsPath, http.MethodPost, http.StatusTooManyRequests, headerRetryAfter))
}

func TestOFREPConformance_SingleFlag(t *testing.T) {
	t.Parallel()

	spec, err := ofrepconformance.LoadSpec(filepath.Join("testdata", "openapi.yaml"))
	require.NoError(t, err)

	baseURL := startRelay(t, "bootstrap.yaml")
	authURL := startRelay(t, "bootstrap-authenticated.yaml")

	tests := []struct {
		headers     map[string]string
		wantFields  map[string]any
		name        string
		targetURL   string
		flagKey     string
		body        string
		expectedBug string
		wantStatus  int
	}{
		{
			name:       "bool targeting match",
			targetURL:  baseURL,
			flagKey:    flagBoolMatch,
			body:       defaultCtxJSON,
			wantStatus: http.StatusOK,
			wantFields: map[string]any{
				jsonKeyField:     flagBoolMatch,
				jsonValueField:   true,
				jsonReasonField:  reasonTargetMatch,
				jsonVariantField: variantTrue,
			},
		},
		{
			name:       "string targeting match",
			targetURL:  baseURL,
			flagKey:    "string_key",
			body:       defaultCtxJSON,
			wantStatus: http.StatusOK,
			wantFields: map[string]any{
				jsonKeyField:     "string_key",
				jsonValueField:   "CC0000",
				jsonReasonField:  reasonTargetMatch,
				jsonVariantField: variantTrue,
			},
		},
		{
			name:       "integer targeting match",
			targetURL:  baseURL,
			flagKey:    "integer_key",
			body:       defaultCtxJSON,
			wantStatus: http.StatusOK,
			wantFields: map[string]any{
				jsonKeyField:     "integer_key",
				jsonValueField:   float64(100),
				jsonReasonField:  reasonTargetMatch,
				jsonVariantField: variantTrue,
			},
		},
		{
			name:       "float targeting match",
			targetURL:  baseURL,
			flagKey:    "double_key",
			body:       defaultCtxJSON,
			wantStatus: http.StatusOK,
			wantFields: map[string]any{
				jsonKeyField:     "double_key",
				jsonValueField:   100.25,
				jsonReasonField:  reasonTargetMatch,
				jsonVariantField: variantTrue,
			},
		},
		{
			name:       "object targeting match",
			targetURL:  baseURL,
			flagKey:    "object_key",
			body:       defaultCtxJSON,
			wantStatus: http.StatusOK,
			wantFields: map[string]any{
				jsonKeyField:     "object_key",
				jsonReasonField:  reasonTargetMatch,
				jsonVariantField: variantTrue,
			},
		},
		{
			name:       "empty context without targetingKey",
			targetURL:  baseURL,
			flagKey:    flagEnrichment,
			body:       `{"context":{}}`,
			wantStatus: http.StatusOK,
			wantFields: map[string]any{
				jsonKeyField:    flagEnrichment,
				jsonValueField:  "A",
				jsonReasonField: reasonTargetMatch,
			},
		},
		{
			name:       "bucketing flag without targetingKey 400",
			targetURL:  baseURL,
			flagKey:    flagBoolMatch,
			body:       `{"context":{"email":"john.doe@gofeatureflag.org"}}`,
			wantStatus: http.StatusBadRequest,
			wantFields: map[string]any{jsonKeyField: flagBoolMatch, jsonErrorCodeField: "TARGETING_KEY_MISSING"},
		},
		{
			name:       "context enrichment on bootstrap.yaml",
			targetURL:  baseURL,
			flagKey:    flagEnrichment,
			body:       `{"context":{"targetingKey":"user-1"}}`,
			wantStatus: http.StatusOK,
			wantFields: map[string]any{
				jsonKeyField:    flagEnrichment,
				jsonValueField:  "A",
				jsonReasonField: reasonTargetMatch,
			},
		},
		{
			name:       "semver targeting match",
			targetURL:  baseURL,
			flagKey:    flagSemver,
			body:       defaultCtxJSON,
			wantStatus: http.StatusOK,
			wantFields: map[string]any{
				jsonKeyField:    flagSemver,
				jsonValueField:  true,
				jsonReasonField: reasonTargetMatch,
			},
		},
		{
			// Expected failure: #134 (returns "value": null instead of omitting "value").
			name:        "disabled flag",
			targetURL:   baseURL,
			flagKey:     "disabled_bool",
			body:        defaultCtxJSON,
			wantStatus:  http.StatusOK,
			expectedBug: "#134",
		},
		{
			// Expected failure: #135 (returns "reason": "DEFAULT", invalid in OFREP 0.4.0).
			name:        "default rule fallback",
			targetURL:   baseURL,
			flagKey:     flagSemver,
			body:        `{"context":{"targetingKey":"user-1","version":"10.0.0-2"}}`,
			wantStatus:  http.StatusOK,
			expectedBug: "#135",
		},
		{
			name:       "flag not found 404",
			targetURL:  baseURL,
			flagKey:    "does_not_exists",
			body:       defaultCtxJSON,
			wantStatus: http.StatusNotFound,
			wantFields: map[string]any{jsonKeyField: "does_not_exists", jsonErrorCodeField: "FLAG_NOT_FOUND"},
		},
		{
			name:       "missing context 400",
			targetURL:  baseURL,
			flagKey:    flagBoolMatch,
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
			wantFields: map[string]any{jsonKeyField: flagBoolMatch, jsonErrorCodeField: "INVALID_CONTEXT"},
		},
		{
			name:       "malformed JSON 400",
			targetURL:  baseURL,
			flagKey:    flagBoolMatch,
			body:       `{`,
			wantStatus: http.StatusBadRequest,
			wantFields: map[string]any{jsonKeyField: flagBoolMatch, jsonErrorCodeField: "INVALID_CONTEXT"},
		},
		{
			name:       "non-string targetingKey 400",
			targetURL:  baseURL,
			flagKey:    flagBoolMatch,
			body:       `{"context":{"targetingKey":123}}`,
			wantStatus: http.StatusBadRequest,
			wantFields: map[string]any{jsonKeyField: flagBoolMatch, jsonErrorCodeField: "TARGETING_KEY_MISSING"},
		},
		{
			name:       "authenticated valid X-API-Key",
			targetURL:  authURL,
			flagKey:    flagBoolMatch,
			headers:    map[string]string{headerXAPIKey: validToken},
			body:       defaultCtxJSON,
			wantStatus: http.StatusOK,
			wantFields: map[string]any{jsonKeyField: flagBoolMatch, jsonValueField: true},
		},
		{
			name:       "authenticated valid Bearer token",
			targetURL:  authURL,
			flagKey:    flagBoolMatch,
			headers:    map[string]string{headerAuth: "Bearer " + validToken},
			body:       defaultCtxJSON,
			wantStatus: http.StatusOK,
			wantFields: map[string]any{jsonKeyField: flagBoolMatch, jsonValueField: true},
		},
		{
			name:       "authenticated missing auth 401",
			targetURL:  authURL,
			flagKey:    flagBoolMatch,
			body:       defaultCtxJSON,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "authenticated invalid X-API-Key 401",
			targetURL:  authURL,
			flagKey:    flagBoolMatch,
			headers:    map[string]string{headerXAPIKey: "wrong"},
			body:       defaultCtxJSON,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "authenticated invalid Bearer 401",
			targetURL:  authURL,
			flagKey:    flagBoolMatch,
			headers:    map[string]string{headerAuth: "Bearer wrong"},
			body:       defaultCtxJSON,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			headers := tt.headers
			if headers == nil && tt.targetURL == baseURL {
				headers = map[string]string{headerXAPIKey: validToken}
			}

			status, hdrs, body := postOFREPRequest(
				t,
				tt.targetURL+"/ofrep/v1/evaluate/flags/"+tt.flagKey,
				headers,
				tt.body,
			)
			assert.Equal(t, tt.wantStatus, status, "body: %s", string(body))

			valErr := spec.ValidateResponse(singleFlagPath, http.MethodPost, status, hdrs, body)
			if tt.expectedBug != "" {
				require.Error(
					t,
					valErr,
					"expected conformance failure for %s; remove expectedBug if fixed",
					tt.expectedBug,
				)

				return
			}

			require.NoError(t, valErr, "body: %s", string(body))

			if len(tt.wantFields) > 0 {
				var parsed map[string]any
				require.NoError(t, json.Unmarshal(body, &parsed))

				for key, want := range tt.wantFields {
					assert.Equal(t, want, parsed[key])
				}
			}
		})
	}
}

func TestOFREPConformance_Bulk(t *testing.T) {
	t.Parallel()

	spec, err := ofrepconformance.LoadSpec(filepath.Join("testdata", "openapi.yaml"))
	require.NoError(t, err)

	baseURL := startRelay(t, "bootstrap.yaml")
	authURL := startRelay(t, "bootstrap-authenticated.yaml")

	t.Run("active flags ETag and If-None-Match 304", func(t *testing.T) {
		t.Parallel()

		authHdr := map[string]string{headerXAPIKey: validToken}
		status, hdrs, body := postOFREPRequest(t, baseURL+bulkFlagsPath, authHdr, activeFlagListCtx)
		require.Equal(t, http.StatusOK, status, "body: %s", string(body))
		require.NoError(t, spec.ValidateResponse(bulkFlagsPath, http.MethodPost, status, hdrs, body))

		etag := hdrs.Get("ETag")
		require.NotEmpty(t, etag)

		status304, hdrs304, body304 := postOFREPRequest(
			t, baseURL+bulkFlagsPath,
			map[string]string{headerXAPIKey: validToken, headerIfNoneMatch: etag}, activeFlagListCtx,
		)
		assert.Equal(t, http.StatusNotModified, status304, "body: %s", string(body304))
		assert.Equal(t, etag, hdrs304.Get("ETag"))
		require.NoError(t, spec.ValidateResponse(bulkFlagsPath, http.MethodPost, status304, hdrs304, body304))

		statusWeak, hdrsWeak, bodyWeak := postOFREPRequest(
			t, baseURL+bulkFlagsPath,
			map[string]string{headerXAPIKey: validToken, headerIfNoneMatch: "W/" + etag}, activeFlagListCtx,
		)
		assert.Equal(t, http.StatusNotModified, statusWeak, "body: %s", string(bodyWeak))
		require.NoError(t, spec.ValidateResponse(bulkFlagsPath, http.MethodPost, statusWeak, hdrsWeak, bodyWeak))

		statusStale, hdrsStale, bodyStale := postOFREPRequest(
			t, baseURL+bulkFlagsPath,
			map[string]string{headerXAPIKey: validToken, headerIfNoneMatch: `"stale"`}, activeFlagListCtx,
		)
		assert.Equal(t, http.StatusOK, statusStale)
		require.NoError(t, spec.ValidateResponse(bulkFlagsPath, http.MethodPost, statusStale, hdrsStale, bodyStale))
	})

	tests := []struct {
		headers     map[string]string
		name        string
		targetURL   string
		body        string
		expectedBug string
		wantStatus  int
	}{
		{
			// Expected failure: #134 (disabled flags in store emit "value": null).
			name:        "full store bulk evaluation",
			targetURL:   baseURL,
			body:        defaultCtxJSON,
			wantStatus:  http.StatusOK,
			expectedBug: "#134",
		},
		{
			// Expected failure: #135 (default-rule fallback emits "reason": "DEFAULT").
			name:        "bulk default rule fallback",
			targetURL:   baseURL,
			body:        `{"context":{"targetingKey":"user-1","gofeatureflag":{"flagList":["bool_targeting_match"]}}}`,
			wantStatus:  http.StatusOK,
			expectedBug: "#135",
		},
		{
			name:       "bulk individual flag error item",
			targetURL:  baseURL,
			body:       `{"context":{"gofeatureflag":{"flagList":["bool_targeting_match"]}}}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing context 400",
			targetURL:  baseURL,
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "malformed JSON 400",
			targetURL:  baseURL,
			body:       `{`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "authenticated valid X-API-Key",
			targetURL:  authURL,
			headers:    map[string]string{headerXAPIKey: validToken},
			body:       activeFlagListCtx,
			wantStatus: http.StatusOK,
		},
		{
			name:       "authenticated valid Bearer",
			targetURL:  authURL,
			headers:    map[string]string{headerAuth: "Bearer " + validToken},
			body:       activeFlagListCtx,
			wantStatus: http.StatusOK,
		},
		{
			name:       "authenticated missing auth 401",
			targetURL:  authURL,
			body:       activeFlagListCtx,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "authenticated invalid key 401",
			targetURL:  authURL,
			headers:    map[string]string{headerXAPIKey: "wrong"},
			body:       activeFlagListCtx,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			headers := tt.headers
			if headers == nil && tt.targetURL == baseURL {
				headers = map[string]string{headerXAPIKey: validToken}
			}

			status, hdrs, body := postOFREPRequest(t, tt.targetURL+bulkFlagsPath, headers, tt.body)
			assert.Equal(t, tt.wantStatus, status, "body: %s", string(body))

			valErr := spec.ValidateResponse(bulkFlagsPath, http.MethodPost, status, hdrs, body)
			if tt.expectedBug != "" {
				require.Error(
					t,
					valErr,
					"expected conformance failure for %s; remove expectedBug if fixed",
					tt.expectedBug,
				)

				return
			}

			require.NoError(t, valErr, "body: %s", string(body))
		})
	}
}
