package corehttp_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	pathOFREPBulk = "/ofrep/v1/evaluate/flags"
	pathOFREPOne  = "/ofrep/v1/evaluate/flags/"
	pathEval      = "/v1/feature/%s/eval"
	pathAllFlags  = "/v1/allflags"
	pathConfig    = "/v1/flag/configuration"
	pathChange    = "/v1/flag/change"
	pathManifest  = "/openfeature/v0/manifest"
	pathCollector = "/v1/data/collector"
	pathRefresh   = "/admin/v1/retriever/refresh"
	xAPIKey       = "X-Api-Key"
	contentType   = "Content-Type"
	authorization = "Authorization"
)

// allowance is a difference between the v1 and the v2 relay that is intended. A case that differs without one fails.
type allowance struct {
	// reason says why the difference is intended.
	reason string
	// unorderedBody: the flags of the body come in the order of a map iteration, in v1 as in v2: they are compared
	// sorted, and the ETag and the length, which hash that order, are not compared.
	unorderedBody bool
}

type equivCase struct {
	allow   *allowance
	headers http.Header
	name    string
	method  string
	path    string
	body    string
}

func keyHeader(key string) http.Header {
	return http.Header{xAPIKey: {key}, contentType: {"application/json"}}
}

func withJSON(h http.Header) http.Header {
	h.Set(contentType, "application/json")

	return h
}

func unorderedBulk() *allowance {
	return &allowance{
		reason: "the flags of a bulk evaluation are listed in map order, in v1 as in v2: compared sorted, " +
			"the ETag and the length hash that order",
		unorderedBody: true,
	}
}

func bearer(key string) http.Header {
	return http.Header{authorization: {"Bearer " + key}, contentType: {"application/json"}}
}

func post(name, path, body string) equivCase {
	return equivCase{name: name, method: http.MethodPost, path: path, body: body, headers: keyHeader(tenantKey)}
}

func ofrep(name, flag, body string) equivCase {
	return post(name, pathOFREPOne+flag, body)
}

func evalCases() []equivCase {
	vip := `{"context":{"targetingKey":"user-1","tier":"vip"}}`
	plain := `{"context":{"targetingKey":"user-1"}}`
	legacy := func(name, flag, body string) equivCase {
		return post(name, strings.Replace(pathEval, "%s", flag, 1), body)
	}

	cases := []equivCase{
		ofrep("ofrep bool vip", "bool-flag", vip),
		ofrep("ofrep bool default", "bool-flag", plain),
		ofrep("ofrep string", "string-flag", plain),
		ofrep("ofrep number rule", "number-flag", `{"context":{"targetingKey":"u","size":11}}`),
		ofrep("ofrep number default", "number-flag", `{"context":{"targetingKey":"u","size":1}}`),
		ofrep("ofrep json object", "json-flag", plain),
		ofrep("ofrep disabled flag", "disabled-flag", plain),
		ofrep("ofrep unknown flag", "no-such-flag", plain),
		ofrep("ofrep rule without targeting key", "bool-flag", `{"context":{"tier":"vip"}}`),
		ofrep("ofrep bucket without targeting key", "bucket-flag", `{"context":{"tier":"vip"}}`),
		ofrep("ofrep no context", "bool-flag", `{}`),
		ofrep("ofrep invalid json", "bool-flag", `{`),
		ofrep("ofrep bucket user 1", "bucket-flag", `{"context":{"targetingKey":"user-1"}}`),
		ofrep("ofrep bucket user 2", "bucket-flag", `{"context":{"targetingKey":"user-2"}}`),
		ofrep("ofrep bucket user 3", "bucket-flag", `{"context":{"targetingKey":"user-3"}}`),
		ofrep("ofrep scheduled over", "scheduled-over-flag", plain),
		ofrep("ofrep scheduled future", "scheduled-future-flag", plain),
		ofrep("ofrep reserved context keys", "bool-flag",
			`{"context":{"targetingKey":"u","tier":"vip","semaphore":{"exporterMetadata":{"k":"v"}}}}`),
		post("ofrep bulk", pathOFREPBulk, vip),
		post("ofrep bulk no targeting key", pathOFREPBulk, `{"context":{"tier":"vip"}}`),
		post("ofrep bulk flag list", pathOFREPBulk,
			`{"context":{"targetingKey":"u","semaphore":{"flagList":["bool-flag","no-such-flag","string-flag"]}}}`),
		post("ofrep bulk legacy flag list", pathOFREPBulk,
			`{"context":{"targetingKey":"u","gofeatureflag":{"flagList":["bool-flag"]}}}`),
		legacy("legacy bool vip", "bool-flag",
			`{"evaluationContext":{"key":"u","custom":{"tier":"vip"}},"defaultValue":false}`),
		legacy("legacy bool default", "bool-flag", `{"evaluationContext":{"key":"u"},"defaultValue":false}`),
		legacy("legacy default of another type", "bool-flag", `{"evaluationContext":{"key":"u"},"defaultValue":"text"}`),
		legacy("legacy number", "number-flag",
			`{"evaluationContext":{"key":"u","custom":{"size":50}},"defaultValue":0}`),
		legacy("legacy json", "json-flag", `{"evaluationContext":{"key":"u"},"defaultValue":{}}`),
		legacy("legacy disabled", "disabled-flag", `{"evaluationContext":{"key":"u"},"defaultValue":7}`),
		legacy("legacy unknown flag", "no-such-flag", `{"evaluationContext":{"key":"u"},"defaultValue":"fallback"}`),
		legacy("legacy rule without key", "bool-flag",
			`{"evaluationContext":{"custom":{"tier":"vip"}},"defaultValue":false}`),
		legacy("legacy bucket without key", "bucket-flag",
			`{"evaluationContext":{"custom":{"tier":"vip"}},"defaultValue":"x"}`),
		legacy("legacy invalid body", "bool-flag", `{`),
		legacy("legacy scheduled over", "scheduled-over-flag", `{"evaluationContext":{"key":"u"},"defaultValue":""}`),
		post("allflags", pathAllFlags, `{"evaluationContext":{"key":"u","custom":{"tier":"vip"}}}`),
		post("allflags key missing", pathAllFlags, `{"evaluationContext":{"custom":{"tier":"vip"}}}`),
		post("allflags flag list", pathAllFlags,
			`{"evaluationContext":{"key":"u","semaphore":{"flagList":["string-flag","bool-flag"]}}}`),
		post("flag configuration", pathConfig, `{}`),
		post("flag configuration subset", pathConfig, `{"flags":["bool-flag","no-such-flag"]}`),
		post("flag configuration invalid", pathConfig, `{`),
		{name: "flag change", method: http.MethodGet, path: pathChange, headers: keyHeader(tenantKey)},
		{name: "manifest", method: http.MethodGet, path: pathManifest, headers: keyHeader(tenantKey)},
		post("collector empty", pathCollector, `{"events":[]}`),
		post("collector events", pathCollector,
			`{"events":[{"kind":"feature","contextKind":"user","userKey":"u","creationDate":1680246000,`+
				`"key":"bool-flag","variation":"enabled","value":true,"default":false,"version":"1","source":"SERVER"}]}`),
	}

	return cases
}

func authCases() []equivCase {
	body := `{"context":{"targetingKey":"u"}}`
	path := pathOFREPOne + "bool-flag"
	request := func(name string, headers http.Header) equivCase {
		return equivCase{name: name, method: http.MethodPost, path: path, body: body, headers: headers}
	}

	return []equivCase{
		request("auth X-API-Key", keyHeader(tenantKey)),
		request("auth bearer", bearer(tenantKey)),
		request("auth bearer lower case", withJSON(http.Header{authorization: {"bearer " + tenantKey}})),
		request("auth bearer with spaces", withJSON(http.Header{authorization: {"Bearer   " + tenantKey + "  "}})),
		request("auth raw authorization", withJSON(http.Header{authorization: {tenantKey}})),
		request("auth X-API-Key wins over authorization",
			withJSON(http.Header{xAPIKey: {tenantKey}, authorization: {"Bearer unknown-key"}})),
		request("auth bad X-API-Key wins over authorization",
			withJSON(http.Header{xAPIKey: {"unknown-key"}, authorization: {"Bearer " + tenantKey}})),
		request("auth no key", withJSON(http.Header{})),
		request("auth empty key", keyHeader("")),
		request("auth unknown key", keyHeader("unknown-key")),
		request("auth unknown bearer", bearer("unknown-key")),
		request("auth admin key", keyHeader(adminKey)),
		{name: "auth unknown key on bulk", method: http.MethodPost, path: pathOFREPBulk, body: body,
			headers: keyHeader("unknown-key"), allow: unorderedBulk()},
		{name: "auth unknown key on legacy eval", method: http.MethodPost, path: "/v1/feature/bool-flag/eval",
			body: `{"evaluationContext":{"key":"u"},"defaultValue":false}`, headers: keyHeader("unknown-key")},
		{name: "auth unknown key on allflags", method: http.MethodPost, path: pathAllFlags,
			body: `{"evaluationContext":{"key":"u"}}`, headers: keyHeader("unknown-key")},
		{name: "auth unknown key on flag change", method: http.MethodGet, path: pathChange, headers: keyHeader("x")},
		{name: "auth admin key on flag change", method: http.MethodGet, path: pathChange, headers: keyHeader(adminKey)},
		{name: "auth no key on manifest", method: http.MethodGet, path: pathManifest, headers: http.Header{}},
		{name: "refresh admin key", method: http.MethodPost, path: pathRefresh, headers: keyHeader(adminKey)},
		{name: "refresh user key", method: http.MethodPost, path: pathRefresh, headers: keyHeader(tenantKey)},
		{name: "refresh unknown key", method: http.MethodPost, path: pathRefresh, headers: keyHeader("x")},
	}
}

// allCases are the requests that both servers answer. The allowances are the intended differences.
func allCases() []equivCase {
	cases := append(evalCases(), authCases()...)

	for idx := range cases {
		if cases[idx].path == pathOFREPBulk && cases[idx].allow == nil {
			cases[idx].allow = unorderedBulk()
		}
	}

	return cases
}

// normalized decodes a JSON body; the flags array of a bulk evaluation is sorted by key when the order is not stable.
func normalized(t *testing.T, body []byte, unordered bool) any {
	t.Helper()

	if len(body) == 0 {
		return nil
	}

	var doc any

	err := json.Unmarshal(body, &doc)
	if err != nil {
		return string(body)
	}

	if obj, ok := doc.(map[string]any); ok && unordered {
		if list, isList := obj["flags"].([]any); isList {
			slices.SortFunc(list, func(a, b any) int {
				return strings.Compare(keyOf(a), keyOf(b))
			})
		}
	}

	return doc
}

func keyOf(v any) string {
	if obj, ok := v.(map[string]any); ok {
		if key, isString := obj["key"].(string); isString {
			return key
		}
	}

	return ""
}

// contractHeaders are the response headers that are part of the contract.
func contractHeaders() []string {
	return []string{contentType, "Etag", "Last-Modified", "Cache-Control", "Www-Authenticate", "Allow", "Vary"}
}

// TestEquivalence_V1V2 serves the same flags from a v1 relay and from a v2 core, with the same handlers, and asks both
// the same questions. The status, the contract headers and the JSON body must be identical, a difference needs an
// allowance with its reason.
func TestEquivalence_V1V2(t *testing.T) {
	t.Parallel()

	v1URL := newV1(t)
	v2URL := newV2(t).url

	for _, tt := range allCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status1, header1, body1 := do(t, tt.method, v1URL+tt.path, tt.body, tt.headers)
			status2, header2, body2 := do(t, tt.method, v2URL+tt.path, tt.body, tt.headers)

			allow := allowance{}
			if tt.allow != nil {
				allow = *tt.allow
				require.NotEmpty(t, allow.reason, "an allowance needs its reason")
			}

			expected, found := expectations()[tt.name]
			require.True(t, found, "a case needs its expectation")
			assert.Equal(t, expected.status, status1, "v1 status")
			assert.Contains(t, string(body1), expected.body, "v1 body")
			assert.Equal(t, status1, status2, "status")

			for _, name := range contractHeaders() {
				if allow.unorderedBody && name == "Etag" {
					assert.Equal(t, header1.Get(name) != "", header2.Get(name) != "", "presence of %s", name)

					continue
				}

				assert.Equal(t, header1.Values(name), header2.Values(name), name)
			}

			want := normalized(t, body1, allow.unorderedBody)
			assert.Equal(t, want, normalized(t, body2, allow.unorderedBody), "body")
		})
	}
}

// expectation is what a case must answer, on both servers: a row that is named for a property (disabled, scheduled,
// bucket, ...) must show it, so a missing fixture or a changed default fails the row instead of passing as an equal
// error on both sides.
type expectation struct {
	// body is a text that the body contains.
	body   string
	status int
}

func expectations() map[string]expectation {
	const (
		notFound = http.StatusNotFound
		ok       = http.StatusOK
		bad      = http.StatusBadRequest
		denied   = http.StatusUnauthorized
	)

	return map[string]expectation{
		"ofrep bool vip":                             {`"value":true,"reason":"TARGETING_MATCH","variant":"enabled"`, ok},
		"ofrep bool default":                         {`"value":false,"reason":"DEFAULT","variant":"disabled"`, ok},
		"ofrep string":                               {`"value":"alpha","reason":"STATIC"`, ok},
		"ofrep number rule":                          {`"value":42,"reason":"TARGETING_MATCH","variant":"high"`, ok},
		"ofrep number default":                       {`"value":1,"reason":"DEFAULT","variant":"low"`, ok},
		"ofrep json object":                          {`"value":{"items":[1,2],"name":"on","nested":{"deep":true}}`, ok},
		"ofrep disabled flag":                        {`"value":null,"reason":"DISABLED"`, ok},
		"ofrep unknown flag":                         {`"errorCode":"FLAG_NOT_FOUND"`, notFound},
		"ofrep rule without targeting key":           {`"reason":"TARGETING_MATCH"`, ok},
		"ofrep bucket without targeting key":         {`TARGETING_KEY_MISSING`, bad},
		"ofrep no context":                           {`"errorCode":"INVALID_CONTEXT"`, bad},
		"ofrep invalid json":                         {`"errorCode":"INVALID_CONTEXT"`, bad},
		"ofrep bucket user 1":                        {`"value":"a","reason":"SPLIT","variant":"a"`, ok},
		"ofrep bucket user 2":                        {`"value":"b","reason":"SPLIT","variant":"b"`, ok},
		"ofrep bucket user 3":                        {`"value":"b","reason":"SPLIT","variant":"b"`, ok},
		"ofrep scheduled over":                       {`"value":"new"`, ok},
		"ofrep scheduled future":                     {`"value":"old"`, ok},
		"ofrep reserved context keys":                {`"value":true,"reason":"TARGETING_MATCH"`, ok},
		"ofrep bulk":                                 {`"key":"scheduled-over-flag","value":"new"`, ok},
		"ofrep bulk no targeting key":                {`"errorCode":"TARGETING_KEY_MISSING"`, ok},
		"ofrep bulk flag list":                       {`{"flags":[{"key":"bool-flag","value":false`, ok},
		"ofrep bulk legacy flag list":                {`{"flags":[{"key":"bool-flag","value":false,"reason":"DEFAULT","variant":"disabled","metadata"`, ok},
		"legacy bool vip":                            {`"variationType":"enabled","failed":false,"version":"","reason":"TARGETING_MATCH"`, ok},
		"legacy bool default":                        {`"variationType":"disabled","failed":false,"version":"","reason":"DEFAULT"`, ok},
		"legacy default of another type":             {`"reason":"DEFAULT","errorCode":"","value":false`, ok},
		"legacy number":                              {`"variationType":"high"`, ok},
		"legacy json":                                {`"value":{"items":[1,2],"name":"on","nested":{"deep":true}}`, ok},
		"legacy disabled":                            {`"reason":"DISABLED","errorCode":"","value":7`, ok},
		"legacy unknown flag":                        {`"failed":true,"version":"","reason":"ERROR","errorCode":"FLAG_NOT_FOUND","value":"fallback"`, ok},
		"legacy rule without key":                    {`"reason":"TARGETING_MATCH"`, ok},
		"legacy bucket without key":                  {`TARGETING_KEY_MISSING`, ok},
		"legacy invalid body":                        {`Bad Request`, bad},
		"legacy scheduled over":                      {`"variationType":"new"`, ok},
		"allflags":                                   {`"scheduled-over-flag":{"value":"new"`, ok},
		"allflags key missing":                       {`"errorCode":"TARGETING_KEY_MISSING"`, ok},
		"allflags flag list":                         {`"string-flag":{"value":"alpha"`, ok},
		"flag configuration":                         {`"scheduledRollout"`, ok},
		"flag configuration subset":                  {`{"flags":{"bool-flag":{"variations"`, ok},
		"flag configuration invalid":                 {`"errorCode":"INVALID_REQUEST"`, bad},
		"flag change":                                {`"bool-flag":`, ok},
		"manifest":                                   {`"key":"bool-flag","type":"boolean","defaultValue":false`, ok},
		"collector empty":                            {`"ingestedContentCount":0`, ok},
		"collector events":                           {`"ingestedContentCount":1`, ok},
		"auth X-API-Key":                             {`"key":"bool-flag"`, ok},
		"auth bearer":                                {`"key":"bool-flag"`, ok},
		"auth bearer lower case":                     {`"key":"bool-flag"`, ok},
		"auth bearer with spaces":                    {`invalid key`, denied},
		"auth raw authorization":                     {`invalid value in request header`, denied},
		"auth X-API-Key wins over authorization":     {`"key":"bool-flag"`, ok},
		"auth bad X-API-Key wins over authorization": {`"message"`, denied},
		"auth no key":                                {`missing value in request header`, denied},
		"auth empty key":                             {`missing value in request header`, denied},
		"auth unknown key":                           {`"message":"Unauthorized"`, denied},
		"auth unknown bearer":                        {`invalid key`, denied},
		"auth admin key":                             {`error while getting flagset: flagset not found for API key`, bad},
		"auth unknown key on bulk":                   {`"message"`, denied},
		"auth unknown key on legacy eval":            {`"message"`, denied},
		"auth unknown key on allflags":               {`"message"`, denied},
		"auth unknown key on flag change":            {`"message":"Unauthorized"`, denied},
		"auth admin key on flag change":              {`error while getting flagset: flagset not found for API key`, bad},
		"auth no key on manifest":                    {`missing value in request header`, denied},
		"refresh admin key":                          {`error while getting flagset: flagset not found for API key`, bad},
		"refresh user key":                           {`"message":"Unauthorized"`, denied},
		"refresh unknown key":                        {`"message":"Unauthorized"`, denied},
	}
}

// TestEquivalence_ExpectationsCoverTheCases keeps the table of expectations and the table of cases the same list.
func TestEquivalence_ExpectationsCoverTheCases(t *testing.T) {
	t.Parallel()

	wants := expectations()
	names := make([]string, 0, len(wants))

	for _, tt := range allCases() {
		names = append(names, tt.name)
	}

	wantNames := make([]string, 0, len(wants))
	for name := range wants {
		wantNames = append(wantNames, name)
	}

	assert.ElementsMatch(t, names, wantNames)
	assert.Len(t, slices.Compact(slices.Sorted(slices.Values(names))), len(names), "case names are unique")
}
