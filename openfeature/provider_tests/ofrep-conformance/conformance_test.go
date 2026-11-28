package ofrepconformance_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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

	flagBoolMatch         = "bool_targeting_match"
	flagDisabledBool      = "disabled_bool"
	flagDisabledFloat     = "disabled_float"
	flagDisabledInt       = "disabled_int"
	flagDisabledInterface = "disabled_interface"
	flagDisabledString    = "disabled_string"
	flagStringKey         = "string_key"
	flagIntegerKey        = "integer_key"
	flagDoubleKey         = "double_key"
	flagObjectKey         = "object_key"
	flagEnrichment        = "flag-use-evaluation-context-enrichment"
	flagSemver            = "boolean_semver_targeting_match"

	errCodeTargetingKeyMissing = "TARGETING_KEY_MISSING"
	errCodeInvalidContext      = "INVALID_CONTEXT"
	contentTypeJSON            = "application/json"
	errSubstrOneOfFailed       = "value matched zero oneOf branches"
	bugDefaultReason           = "#135"
	reasonDisabled             = "DISABLED"
	reasonDefault              = "DEFAULT"
	reasonStatic               = "STATIC"
	crlfPairLen                = 4

	defaultCtxJSON    = `{"context":{"targetingKey":"user-1","email":"john.doe@gofeatureflag.org","version":"10.0.0-10"}}`
	activeFlagListCtx = `{"context":{"targetingKey":"user-1","email":"john.doe@gofeatureflag.org",` +
		`"gofeatureflag":{"flagList":["bool_targeting_match","string_key","integer_key","double_key","object_key"]}}}`
)

func postOFREPRequest(
	t *testing.T,
	rawURL string,
	headers map[string]string,
	body string,
) (int, http.Header, []byte) {
	t.Helper()

	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, rawURL, reader)
	require.NoError(t, err)

	if body != "" {
		req.Header.Set("Content-Type", contentTypeJSON)
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

// postRawHTTP1 reads the raw HTTP/1.1 wire bytes after the header block before net/http strips 304 bodies.
func postRawHTTP1(
	t *testing.T,
	rawURL string,
	headers map[string]string,
	body string,
) (int, http.Header, []byte) {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), httpClientTimeout)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", parsed.Host)
	require.NoError(t, err)

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer func() { _ = conn.Close() }()

	var reqBuf bytes.Buffer
	fmt.Fprintf(&reqBuf, "POST %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n", parsed.RequestURI(), parsed.Host)

	for key, val := range headers {
		fmt.Fprintf(&reqBuf, "%s: %s\r\n", key, val)
	}

	if body != "" {
		fmt.Fprintf(&reqBuf, "Content-Type: %s\r\nContent-Length: %d\r\n", contentTypeJSON, len(body))
	}

	reqBuf.WriteString("\r\n")
	reqBuf.WriteString(body)

	_, err = conn.Write(reqBuf.Bytes())
	require.NoError(t, err)

	raw, err := io.ReadAll(conn)
	require.NoError(t, err)

	headerEnd := bytes.Index(raw, []byte("\r\n\r\n"))
	require.NotEqual(t, -1, headerEnd, "malformed HTTP/1.1 response: %q", string(raw))

	headPart := raw[:headerEnd+crlfPairLen]
	wireBody := raw[headerEnd+crlfPairLen:]

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, http.NoBody)
	require.NoError(t, err)

	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(headPart)), req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	return resp.StatusCode, resp.Header, wireBody
}

func TestOFREPConformance_SpecHeaders(t *testing.T) {
	t.Parallel()

	spec, err := ofrepconformance.LoadSpec(filepath.Join("testdata", "openapi.yaml"))
	require.NoError(t, err)

	assert.True(t, spec.HasHeader(bulkFlagsPath, http.MethodPost, http.StatusOK, "ETag"))
	assert.True(t, spec.HasHeader(singleFlagPath, http.MethodPost, http.StatusTooManyRequests, headerRetryAfter))
	assert.True(t, spec.HasHeader(bulkFlagsPath, http.MethodPost, http.StatusTooManyRequests, headerRetryAfter))
}

type singleFlagCase struct {
	headers          map[string]string
	wantFields       map[string]any
	name             string
	targetURL        string
	flagKey          string
	body             string
	expectedBug      string
	wantErrSubstr    string
	wantAbsentFields []string
	wantStatus       int
}

func singleFlagTestCases(baseURL, authURL string) []singleFlagCase {
	return []singleFlagCase{
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
			flagKey:    flagStringKey,
			body:       defaultCtxJSON,
			wantStatus: http.StatusOK,
			wantFields: map[string]any{
				jsonKeyField:     flagStringKey,
				jsonValueField:   "CC0000",
				jsonReasonField:  reasonTargetMatch,
				jsonVariantField: variantTrue,
			},
		},
		{
			name:       "integer targeting match",
			targetURL:  baseURL,
			flagKey:    flagIntegerKey,
			body:       defaultCtxJSON,
			wantStatus: http.StatusOK,
			wantFields: map[string]any{
				jsonKeyField:     flagIntegerKey,
				jsonValueField:   float64(100),
				jsonReasonField:  reasonTargetMatch,
				jsonVariantField: variantTrue,
			},
		},
		{
			name:       "float targeting match",
			targetURL:  baseURL,
			flagKey:    flagDoubleKey,
			body:       defaultCtxJSON,
			wantStatus: http.StatusOK,
			wantFields: map[string]any{
				jsonKeyField:     flagDoubleKey,
				jsonValueField:   100.25,
				jsonReasonField:  reasonTargetMatch,
				jsonVariantField: variantTrue,
			},
		},
		{
			name:       "object targeting match",
			targetURL:  baseURL,
			flagKey:    flagObjectKey,
			body:       defaultCtxJSON,
			wantStatus: http.StatusOK,
			wantFields: map[string]any{
				jsonKeyField:     flagObjectKey,
				jsonValueField:   expectedObjectValue(),
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
			wantFields: map[string]any{jsonKeyField: flagBoolMatch, jsonErrorCodeField: errCodeTargetingKeyMissing},
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
			name:             "disabled bool flag",
			targetURL:        baseURL,
			flagKey:          flagDisabledBool,
			body:             defaultCtxJSON,
			wantStatus:       http.StatusOK,
			wantAbsentFields: []string{jsonValueField},
			wantFields: map[string]any{
				jsonKeyField:    flagDisabledBool,
				jsonReasonField: reasonDisabled,
			},
		},
		{
			name:             "disabled string flag",
			targetURL:        baseURL,
			flagKey:          flagDisabledString,
			body:             defaultCtxJSON,
			wantStatus:       http.StatusOK,
			wantAbsentFields: []string{jsonValueField},
			wantFields: map[string]any{
				jsonKeyField:    flagDisabledString,
				jsonReasonField: reasonDisabled,
			},
		},
		{
			name:             "disabled int flag",
			targetURL:        baseURL,
			flagKey:          flagDisabledInt,
			body:             defaultCtxJSON,
			wantStatus:       http.StatusOK,
			wantAbsentFields: []string{jsonValueField},
			wantFields: map[string]any{
				jsonKeyField:    flagDisabledInt,
				jsonReasonField: reasonDisabled,
			},
		},
		{
			name:             "disabled float flag",
			targetURL:        baseURL,
			flagKey:          flagDisabledFloat,
			body:             defaultCtxJSON,
			wantStatus:       http.StatusOK,
			wantAbsentFields: []string{jsonValueField},
			wantFields: map[string]any{
				jsonKeyField:    flagDisabledFloat,
				jsonReasonField: reasonDisabled,
			},
		},
		{
			name:             "disabled object flag",
			targetURL:        baseURL,
			flagKey:          flagDisabledInterface,
			body:             defaultCtxJSON,
			wantStatus:       http.StatusOK,
			wantAbsentFields: []string{jsonValueField},
			wantFields: map[string]any{
				jsonKeyField:    flagDisabledInterface,
				jsonReasonField: reasonDisabled,
			},
		},
		{
			// Expected failure: #135 (returns "reason": "DEFAULT", invalid in OFREP 0.4.0).
			name:          "default rule fallback",
			targetURL:     baseURL,
			flagKey:       flagSemver,
			body:          `{"context":{"targetingKey":"user-1","version":"10.0.0-2"}}`,
			wantStatus:    http.StatusOK,
			expectedBug:   bugDefaultReason,
			wantErrSubstr: "property reason: value is not in enum: got DEFAULT",
			wantFields: map[string]any{
				jsonKeyField:    flagSemver,
				jsonValueField:  false,
				jsonReasonField: reasonDefault,
			},
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
			wantFields: map[string]any{jsonKeyField: flagBoolMatch, jsonErrorCodeField: errCodeInvalidContext},
		},
		{
			name:       "malformed JSON 400",
			targetURL:  baseURL,
			flagKey:    flagBoolMatch,
			body:       `{`,
			wantStatus: http.StatusBadRequest,
			wantFields: map[string]any{jsonKeyField: flagBoolMatch, jsonErrorCodeField: errCodeInvalidContext},
		},
		{
			name:       "non-string targetingKey 400",
			targetURL:  baseURL,
			flagKey:    flagBoolMatch,
			body:       `{"context":{"targetingKey":123}}`,
			wantStatus: http.StatusBadRequest,
			wantFields: map[string]any{jsonKeyField: flagBoolMatch, jsonErrorCodeField: errCodeTargetingKeyMissing},
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
}

func patchKnownBugEntry(bug string, entry map[string]any) bool {
	if bug == bugDefaultReason && entry[jsonReasonField] == reasonDefault {
		entry[jsonReasonField] = reasonStatic

		return true
	}

	return false
}

// patchKnownBugBody removes only the tracked relay defect from a cloned JSON payload so the rest
// of the response can still be strictly schema-validated.
func patchKnownBugBody(t *testing.T, bug string, body []byte) []byte {
	t.Helper()

	var root map[string]any
	require.NoError(t, json.Unmarshal(body, &root), "body: %s", string(body))

	patched := 0

	if rawFlags, isBulk := root["flags"]; isBulk {
		items, ok := rawFlags.([]any)
		require.True(t, ok, "expected flags array in bulk response")

		for _, item := range items {
			entry, isObj := item.(map[string]any)
			require.True(t, isObj, "expected object item in flags array")

			if patchKnownBugEntry(bug, entry) {
				patched++
			}
		}
	} else if patchKnownBugEntry(bug, root) {
		patched++
	}

	require.Positive(t, patched, "expected %s defect was not present in payload: %s", bug, string(body))

	fixedBody, err := json.Marshal(root)
	require.NoError(t, err)

	return fixedBody
}

func runSingleFlagCase(t *testing.T, spec *ofrepconformance.Spec, baseURL string, testCase *singleFlagCase) {
	t.Helper()

	headers := testCase.headers
	if headers == nil && testCase.targetURL == baseURL {
		headers = map[string]string{headerXAPIKey: validToken}
	}

	status, hdrs, body := postOFREPRequest(
		t,
		testCase.targetURL+"/ofrep/v1/evaluate/flags/"+testCase.flagKey,
		headers,
		testCase.body,
	)
	assert.Equal(t, testCase.wantStatus, status, "body: %s", string(body))

	valErr := spec.ValidateResponse(singleFlagPath, http.MethodPost, status, hdrs, body)
	if testCase.expectedBug != "" {
		require.Error(
			t,
			valErr,
			"expected conformance failure for %s; remove expectedBug if fixed",
			testCase.expectedBug,
		)
		require.ErrorContains(t, valErr, testCase.wantErrSubstr)
		assert.Contains(t, hdrs.Get("Content-Type"), contentTypeJSON)

		patched := patchKnownBugBody(t, testCase.expectedBug, body)
		require.NoError(t, spec.ValidateResponse(singleFlagPath, http.MethodPost, status, hdrs, patched))
	} else {
		require.NoError(t, valErr, "body: %s", string(body))
	}

	if len(testCase.wantFields) > 0 || len(testCase.wantAbsentFields) > 0 {
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(body, &parsed))

		for key, want := range testCase.wantFields {
			got, exists := parsed[key]
			assert.True(t, exists, "missing field %s", key)
			assert.Equal(t, want, got, "field %s", key)
		}

		for _, key := range testCase.wantAbsentFields {
			assert.NotContains(t, parsed, key, "unexpected field %s", key)
		}
	}
}

func TestOFREPConformance_SingleFlag(t *testing.T) {
	t.Parallel()

	spec, err := ofrepconformance.LoadSpec(filepath.Join("testdata", "openapi.yaml"))
	require.NoError(t, err)

	baseURL := startRelay(t, "bootstrap.yaml")
	authURL := startRelay(t, "bootstrap-authenticated.yaml")

	for _, tt := range singleFlagTestCases(baseURL, authURL) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runSingleFlagCase(t, spec, baseURL, &tt)
		})
	}
}

func expectedObjectValue() map[string]any {
	return map[string]any{
		"test":  "test1",
		"test2": false,
		"test3": 123.3,
		"test4": float64(1),
	}
}

func expectedActiveBulkValues() map[string]any {
	return map[string]any{
		flagBoolMatch:  true,
		flagStringKey:  "CC0000",
		flagIntegerKey: float64(100),
		flagDoubleKey:  100.25,
		flagObjectKey:  expectedObjectValue(),
	}
}

func parseBulkFlags(t *testing.T, body []byte) map[string]map[string]any {
	t.Helper()

	var payload struct {
		Flags []map[string]any `json:"flags"`
	}
	require.NoError(t, json.Unmarshal(body, &payload), "body: %s", string(body))

	byKey := make(map[string]map[string]any, len(payload.Flags))
	for _, entry := range payload.Flags {
		key, ok := entry[jsonKeyField].(string)
		require.True(t, ok, "bulk flag item missing string key: %v", entry)
		require.NotContains(t, byKey, key, "duplicate bulk flag key %q", key)
		byKey[key] = entry
	}

	return byKey
}

func assertActiveBulkFlags(t *testing.T, body []byte) {
	t.Helper()

	byKey := parseBulkFlags(t, body)
	wantValues := expectedActiveBulkValues()
	require.Len(t, byKey, len(wantValues))

	for key, wantVal := range wantValues {
		entry, ok := byKey[key]
		require.True(t, ok, "missing flag %q in bulk response", key)
		assert.Equal(t, wantVal, entry[jsonValueField], "flag %q", key)
	}
}

type bulkTestCase struct {
	headers             map[string]string
	wantFlagErrors      map[string]string
	wantReasonByKey     map[string]string
	name                string
	targetURL           string
	body                string
	expectedBug         string
	wantErrSubstr       string
	wantErrorCode       string
	wantFlagKeys        []string
	wantAbsentValueKeys []string
	wantStatus          int
}

func assertBulkBugResponse(
	t *testing.T,
	spec *ofrepconformance.Spec,
	status int,
	hdrs http.Header,
	body []byte,
	valErr error,
	testCase *bulkTestCase,
) {
	t.Helper()

	require.Error(
		t,
		valErr,
		"expected conformance failure for %s; remove expectedBug if fixed",
		testCase.expectedBug,
	)
	require.ErrorContains(t, valErr, testCase.wantErrSubstr)
	assert.NotEmpty(t, hdrs.Get("ETag"))
	assert.Contains(t, hdrs.Get("Content-Type"), contentTypeJSON)

	flags := parseBulkFlags(t, body)

	for key, wantReason := range testCase.wantReasonByKey {
		entry, ok := flags[key]
		require.True(t, ok, "missing expected flag %q", key)
		assert.Equal(t, wantReason, entry[jsonReasonField], "flag %q", key)
	}

	patched := patchKnownBugBody(t, testCase.expectedBug, body)
	require.NoError(t, spec.ValidateResponse(bulkFlagsPath, http.MethodPost, status, hdrs, patched))
}

func assertBulkValidResponse(t *testing.T, body []byte, valErr error, testCase *bulkTestCase) {
	t.Helper()

	require.NoError(t, valErr, "body: %s", string(body))

	if testCase.wantErrorCode != "" {
		var errBody map[string]any
		require.NoError(t, json.Unmarshal(body, &errBody))
		assert.Equal(t, testCase.wantErrorCode, errBody[jsonErrorCodeField])
	}

	hasFlagChecks := len(testCase.wantFlagKeys) > 0 || len(testCase.wantFlagErrors) > 0 ||
		len(testCase.wantAbsentValueKeys) > 0 || len(testCase.wantReasonByKey) > 0
	if !hasFlagChecks {
		return
	}

	flags := parseBulkFlags(t, body)

	if len(testCase.wantFlagKeys) > 0 {
		assert.Len(t, flags, len(testCase.wantFlagKeys))

		for _, key := range testCase.wantFlagKeys {
			assert.Contains(t, flags, key)
		}
	}

	for key, wantErrCode := range testCase.wantFlagErrors {
		entry, ok := flags[key]
		require.True(t, ok, "missing expected error flag %q", key)
		assert.Equal(t, wantErrCode, entry[jsonErrorCodeField], "flag %q", key)
	}

	for _, key := range testCase.wantAbsentValueKeys {
		entry, ok := flags[key]
		require.True(t, ok, "missing expected flag %q", key)
		assert.NotContains(t, entry, jsonValueField, "flag %q", key)
	}

	for key, wantReason := range testCase.wantReasonByKey {
		entry, ok := flags[key]
		require.True(t, ok, "missing expected flag %q", key)
		assert.Equal(t, wantReason, entry[jsonReasonField], "flag %q", key)
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
		assertActiveBulkFlags(t, body)

		etag := hdrs.Get("ETag")
		require.NotEmpty(t, etag)

		status304, hdrs304, body304 := postRawHTTP1(
			t, baseURL+bulkFlagsPath,
			map[string]string{headerXAPIKey: validToken, headerIfNoneMatch: etag}, activeFlagListCtx,
		)
		assert.Equal(t, http.StatusNotModified, status304, "body: %s", string(body304))
		assert.Empty(t, body304)
		assert.Equal(t, etag, hdrs304.Get("ETag"))
		require.NoError(t, spec.ValidateResponse(bulkFlagsPath, http.MethodPost, status304, hdrs304, body304))

		statusWeak, hdrsWeak, bodyWeak := postRawHTTP1(
			t, baseURL+bulkFlagsPath,
			map[string]string{headerXAPIKey: validToken, headerIfNoneMatch: "W/" + etag}, activeFlagListCtx,
		)
		assert.Equal(t, http.StatusNotModified, statusWeak, "body: %s", string(bodyWeak))
		assert.Empty(t, bodyWeak)
		require.NoError(t, spec.ValidateResponse(bulkFlagsPath, http.MethodPost, statusWeak, hdrsWeak, bodyWeak))

		statusStale, hdrsStale, bodyStale := postOFREPRequest(
			t, baseURL+bulkFlagsPath,
			map[string]string{headerXAPIKey: validToken, headerIfNoneMatch: `"stale"`}, activeFlagListCtx,
		)
		assert.Equal(t, http.StatusOK, statusStale)
		require.NoError(t, spec.ValidateResponse(bulkFlagsPath, http.MethodPost, statusStale, hdrsStale, bodyStale))
		assertActiveBulkFlags(t, bodyStale)
	})

	activeFlagKeys := []string{flagBoolMatch, flagStringKey, flagIntegerKey, flagDoubleKey, flagObjectKey}

	tests := []bulkTestCase{
		{
			name:       "full store bulk evaluation",
			targetURL:  baseURL,
			body:       defaultCtxJSON,
			wantStatus: http.StatusOK,
			wantAbsentValueKeys: []string{
				flagDisabledBool,
				flagDisabledFloat,
				flagDisabledInt,
				flagDisabledInterface,
				flagDisabledString,
			},
			wantReasonByKey: map[string]string{
				flagDisabledBool:      reasonDisabled,
				flagDisabledFloat:     reasonDisabled,
				flagDisabledInt:       reasonDisabled,
				flagDisabledInterface: reasonDisabled,
				flagDisabledString:    reasonDisabled,
			},
		},
		{
			// Expected failure: #135 (default-rule fallback emits "reason": "DEFAULT").
			name:            "bulk default rule fallback",
			targetURL:       baseURL,
			body:            `{"context":{"targetingKey":"user-1","gofeatureflag":{"flagList":["bool_targeting_match"]}}}`,
			wantStatus:      http.StatusOK,
			expectedBug:     bugDefaultReason,
			wantErrSubstr:   errSubstrOneOfFailed,
			wantReasonByKey: map[string]string{flagBoolMatch: reasonDefault},
		},
		{
			name:                "bulk individual flag error item",
			targetURL:           baseURL,
			body:                `{"context":{"gofeatureflag":{"flagList":["bool_targeting_match"]}}}`,
			wantStatus:          http.StatusOK,
			wantFlagKeys:        []string{flagBoolMatch},
			wantFlagErrors:      map[string]string{flagBoolMatch: errCodeTargetingKeyMissing},
			wantAbsentValueKeys: []string{flagBoolMatch},
		},
		{
			name:          "missing context 400",
			targetURL:     baseURL,
			body:          `{}`,
			wantStatus:    http.StatusBadRequest,
			wantErrorCode: errCodeInvalidContext,
		},
		{
			name:          "malformed JSON 400",
			targetURL:     baseURL,
			body:          `{`,
			wantStatus:    http.StatusBadRequest,
			wantErrorCode: errCodeInvalidContext,
		},
		{
			name:         "authenticated valid X-API-Key",
			targetURL:    authURL,
			headers:      map[string]string{headerXAPIKey: validToken},
			body:         activeFlagListCtx,
			wantStatus:   http.StatusOK,
			wantFlagKeys: activeFlagKeys,
		},
		{
			name:         "authenticated valid Bearer",
			targetURL:    authURL,
			headers:      map[string]string{headerAuth: "Bearer " + validToken},
			body:         activeFlagListCtx,
			wantStatus:   http.StatusOK,
			wantFlagKeys: activeFlagKeys,
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
				assertBulkBugResponse(t, spec, status, hdrs, body, valErr, &tt)

				return
			}

			assertBulkValidResponse(t, body, valErr, &tt)
		})
	}
}
