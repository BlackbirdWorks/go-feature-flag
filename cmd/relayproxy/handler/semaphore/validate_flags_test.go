package controller_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/internal/protocol"
)

const (
	applicationToml = "application/toml"
	applicationYaml = "application/yaml"
)

var (
	errReadFailure = errors.New("read failure")
)

const (
	jsonBodyLimit = 1 << 20
	yamlBodyLimit = 64 << 10
	tomlBodyLimit = 64 << 10
)

// failingReader is a request body that always fails to be read.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errReadFailure }

// normalizeAltMessage replaces the single error message equal to alt by the expected one.
func normalizeAltMessage(got []controller.FlagValidationError, alt string, want []controller.FlagValidationError) {
	if alt != "" && len(got) == 1 && got[0].Message == alt {
		got[0].Message = want[0].Message
	}
}

// validateOutcomeCounts returns the validate flags counter value per outcome label.
func validateOutcomeCounts(t *testing.T, m metric.Metrics) map[string]float64 {
	t.Helper()

	families, err := m.Registry.Gather()
	require.NoError(t, err)

	counts := map[string]float64{}

	for _, f := range families {
		if f.GetName() != "semaphore_validate_flags_total" {
			continue
		}

		for _, mm := range f.GetMetric() {
			counts[mm.GetLabel()[0].GetValue()] = mm.GetCounter().GetValue()
		}
	}

	return counts
}

// outcomeForStatus returns the metric outcome expected for an HTTP status.
func outcomeForStatus(status int) string {
	switch status {
	case http.StatusOK:
		return metric.ValidateFlagsOutcomeValid
	case http.StatusUnprocessableEntity:
		return metric.ValidateFlagsOutcomeInvalid
	default:
		return metric.ValidateFlagsOutcomeError
	}
}

// padTo pads body with the pad string so the result is exactly size bytes long.
func padTo(body, pad string, size int) string {
	return body + strings.Repeat(pad, size-len(body))
}

// assertValidateResult checks the handler error (error statuses) or the JSON response (200 and 422).
func assertValidateResult(
	t *testing.T, err error, rec *httptest.ResponseRecorder,
	wantStatus int, wantValid bool, altMessage string, wantErrors []controller.FlagValidationError,
) {
	t.Helper()

	if wantStatus != http.StatusOK && wantStatus != http.StatusUnprocessableEntity {
		var httpErr *helper.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, wantStatus, httpErr.Code)

		if wantStatus == http.StatusRequestEntityTooLarge {
			assert.Contains(t, httpErr.Message, "request body too large (max ")
		}

		return
	}

	require.NoError(t, err)
	assert.Equal(t, wantStatus, rec.Code)

	var got controller.FlagValidationResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "v1.2.3", got.Version)
	assert.Equal(t, wantValid, got.Valid)
	normalizeAltMessage(got.Errors, altMessage, wantErrors)

	if wantErrors == nil {
		wantErrors = []controller.FlagValidationError{}
	}

	assert.Equal(t, wantErrors, got.Errors)
}

func TestNewValidateFlags_Handler(t *testing.T) {
	t.Parallel()

	const (
		validJSON   = `{"flag-a":{"variations":{"a":true,"b":false},"defaultRule":{"variation":"a"}}}`
		validYAML   = "flag-a:\n  variations:\n    a: true\n    b: false\n  defaultRule:\n    variation: a\n"
		validTOML   = "[flag-a]\n[flag-a.variations]\na = true\nb = false\n[flag-a.defaultRule]\nvariation = \"a\"\n"
		invalidJSON = `{"flag-b":{"variations":{"a":true}},"flag-a":{"variations":{"a":true}}}`
	)

	oversizedJSON := padTo(validJSON, " ", jsonBodyLimit+1)

	tests := []struct {
		reader      io.Reader
		name        string
		contentType string
		body        string
		// altMessage is the message returned when modules/core validates the flag without panicking.
		altMessage string
		wantErrors []controller.FlagValidationError
		limits     config.ValidateFlagsEndpoint
		wantStatus int
		chunked    bool
		wantValid  bool
	}{
		{
			name:        "valid json",
			contentType: protocol.MIMEApplicationJSON,
			body:        validJSON,
			wantStatus:  http.StatusOK,
			wantValid:   true,
		},
		{
			name:        "valid json with charset",
			contentType: "application/json; charset=utf-8",
			body:        validJSON,
			wantStatus:  http.StatusOK,
			wantValid:   true,
		},
		{
			name:        "valid yaml",
			contentType: applicationYaml,
			body:        validYAML,
			wantStatus:  http.StatusOK,
			wantValid:   true,
		},
		{
			name:        "valid x-yaml",
			contentType: "application/x-yaml",
			body:        validYAML,
			wantStatus:  http.StatusOK,
			wantValid:   true,
		},
		{
			name:        "valid text yaml",
			contentType: "text/yaml",
			body:        validYAML,
			wantStatus:  http.StatusOK,
			wantValid:   true,
		},
		{
			name:        "valid toml",
			contentType: applicationToml,
			body:        validTOML,
			wantStatus:  http.StatusOK,
			wantValid:   true,
		},
		{
			name:        "empty yaml body",
			contentType: applicationYaml,
			body:        "",
			wantStatus:  http.StatusOK,
			wantValid:   true,
		},
		{
			name: "invalid flags sorted by key", contentType: protocol.MIMEApplicationJSON, body: invalidJSON,
			wantStatus: http.StatusUnprocessableEntity,
			wantErrors: []controller.FlagValidationError{
				{Flag: "flag-a", Message: "validating the flag: missing default rule"},
				{Flag: "flag-b", Message: "validating the flag: missing default rule"},
			},
		},
		{
			name: "panicking flag definition", contentType: protocol.MIMEApplicationJSON,
			body:       `{"f":{"variations":{"a":true},"defaultRule":{"progressiveRollout":{}}}}`,
			altMessage: "validating the flag: invalid progressive rollout, initial and end steps are required",
			wantStatus: http.StatusUnprocessableEntity,
			wantErrors: []controller.FlagValidationError{{
				Flag:    "f",
				Message: "internal validation error: flag definition could not be validated",
			}},
		},
		{
			name:        "bad json syntax",
			contentType: protocol.MIMEApplicationJSON,
			body:        `{"flag-a":`,
			wantStatus:  http.StatusBadRequest,
		},
		{name: "bad yaml syntax", contentType: applicationYaml, body: "a: [", wantStatus: http.StatusBadRequest},
		{name: "bad toml syntax", contentType: applicationToml, body: "a = ", wantStatus: http.StatusBadRequest},
		{
			name:        "yaml scalar",
			contentType: applicationYaml,
			body:        "just a string",
			wantStatus:  http.StatusBadRequest,
		},
		{name: "yaml list", contentType: applicationYaml, body: "- a\n- b\n", wantStatus: http.StatusBadRequest},
		{name: "json list", contentType: protocol.MIMEApplicationJSON, body: "[]", wantStatus: http.StatusBadRequest},
		{
			name:        "unreadable body",
			contentType: protocol.MIMEApplicationJSON,
			reader:      failingReader{},
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "unsupported content type",
			contentType: "text/plain",
			body:        validJSON,
			wantStatus:  http.StatusUnsupportedMediaType,
		},
		{
			name:        "malformed content type",
			contentType: "application/json;;;=",
			body:        validJSON,
			wantStatus:  http.StatusUnsupportedMediaType,
		},
		{name: "missing content type", contentType: "", body: validJSON, wantStatus: http.StatusUnsupportedMediaType},
		{
			name: "json at the size limit", contentType: protocol.MIMEApplicationJSON,
			body: padTo(validJSON, " ", jsonBodyLimit), wantStatus: http.StatusOK, wantValid: true,
		},
		{
			name:        "json over the size limit",
			contentType: protocol.MIMEApplicationJSON,
			body:        oversizedJSON,
			wantStatus:  http.StatusRequestEntityTooLarge,
		},
		{
			name: "chunked json over the size limit", contentType: protocol.MIMEApplicationJSON, chunked: true,
			body: oversizedJSON, wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "yaml at the size limit", contentType: applicationYaml,
			body: padTo(validYAML, "\n", yamlBodyLimit), wantStatus: http.StatusOK, wantValid: true,
		},
		{
			name: "yaml over the size limit", contentType: applicationYaml,
			body: padTo(validYAML, "\n", yamlBodyLimit+1), wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "chunked yaml over the size limit", contentType: applicationYaml, chunked: true,
			body: padTo(validYAML, "\n", yamlBodyLimit+1), wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "toml at the size limit", contentType: applicationToml,
			body: padTo(validTOML, "\n", tomlBodyLimit), wantStatus: http.StatusOK, wantValid: true,
		},
		{
			name: "toml over the size limit", contentType: applicationToml,
			body: padTo(validTOML, "\n", tomlBodyLimit+1), wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "custom json limit, at the limit", contentType: protocol.MIMEApplicationJSON,
			limits: config.ValidateFlagsEndpoint{MaxJSONBodySize: 2048},
			body:   padTo(validJSON, " ", 2048), wantStatus: http.StatusOK, wantValid: true,
		},
		{
			name: "custom json limit, over the limit", contentType: protocol.MIMEApplicationJSON,
			limits: config.ValidateFlagsEndpoint{MaxJSONBodySize: 2048},
			body:   padTo(validJSON, " ", 2049), wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "custom json limit does not change yaml", contentType: applicationYaml,
			limits: config.ValidateFlagsEndpoint{MaxJSONBodySize: 2048},
			body:   padTo(validYAML, "\n", yamlBodyLimit+1), wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "custom yaml limit, at the limit", contentType: applicationYaml,
			limits: config.ValidateFlagsEndpoint{MaxYAMLBodySize: 128 << 10},
			body:   padTo(validYAML, "\n", 128<<10), wantStatus: http.StatusOK, wantValid: true,
		},
		{
			name: "custom yaml limit, over the limit", contentType: applicationYaml, chunked: true,
			limits: config.ValidateFlagsEndpoint{MaxYAMLBodySize: 128 << 10},
			body:   padTo(validYAML, "\n", 128<<10+1), wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "custom toml limit, at the limit", contentType: applicationToml,
			limits: config.ValidateFlagsEndpoint{MaxTOMLBodySize: 1024},
			body:   padTo(validTOML, "\n", 1024), wantStatus: http.StatusOK, wantValid: true,
		},
		{
			name: "custom toml limit, over the limit", contentType: applicationToml,
			limits: config.ValidateFlagsEndpoint{MaxTOMLBodySize: 1024},
			body:   padTo(validTOML, "\n", 1025), wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "custom toml limit smaller than the default", contentType: applicationToml,
			limits: config.ValidateFlagsEndpoint{MaxTOMLBodySize: 1024},
			body:   padTo(validTOML, "\n", tomlBodyLimit), wantStatus: http.StatusRequestEntityTooLarge,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			metrics, err := metric.NewMetrics()
			require.NoError(t, err)

			ctrl := controller.NewValidateFlags("v1.2.3", metrics, tt.limits)
			rec := httptest.NewRecorder()

			req := httptest.NewRequestWithContext(
				t.Context(), http.MethodPost, "/admin/v1/flags/validate", strings.NewReader(tt.body))
			switch {
			case tt.reader != nil:
				req.Body = io.NopCloser(tt.reader)
				req.ContentLength = -1
			case tt.chunked:
				req.Body = io.NopCloser(strings.NewReader(tt.body))
				req.ContentLength = -1
			}

			if tt.contentType != "" {
				req.Header.Set(protocol.HeaderContentType, tt.contentType)
			}

			err = ctrl.Handler(rec, req)

			assert.Equal(t, map[string]float64{outcomeForStatus(tt.wantStatus): 1}, validateOutcomeCounts(t, metrics))
			assertValidateResult(t, err, rec, tt.wantStatus, tt.wantValid, tt.altMessage, tt.wantErrors)
		})
	}
}
