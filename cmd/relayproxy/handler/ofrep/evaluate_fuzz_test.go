package ofrep_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

// FuzzOFREPEvaluate checks that any request body sent to the OFREP endpoints
// gets a JSON answer that is not a server error, and never panics the handler.
func FuzzOFREPEvaluate(f *testing.F) {
	requests, err := filepath.Glob(testdataDir + "/ofrep/*.json")
	require.NoError(f, err)

	// the mux redirects a key that is a dot segment: no body
	f.Add(".", "{}", false)
	f.Add("..", "{}", false)

	for _, file := range requests {
		content, readFileErr := os.ReadFile(file)
		require.NoError(f, readFileErr)
		f.Add(numberFlag, string(content), false)
		f.Add("", string(content), true)
	}

	cfg := mock.FileConfig(configFlagsLocation)
	cfg.FileFormat = protocol.FormatYAML
	flagsetManager := mock.NewDefault(f, &cfg)

	ctrl := ofrep.NewOFREPEvaluate(flagsetManager, &metric.Metrics{}, ofrep.EventStream{})
	mux := http.NewServeMux()
	mux.Handle(postOfrepVOneEvaluate, helper.Handle(ctrl.Evaluate, false))
	mux.Handle("POST /ofrep/v1/evaluate/flags", helper.Handle(ctrl.BulkEvaluate, false))

	f.Fuzz(func(t *testing.T, flagKey, body string, bulk bool) {
		target := ofrepEvaluatePrefix + url.PathEscape(flagKey)
		if bulk {
			target = fixtures.OFREPEvaluateFlagsPath
		}

		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(body))
		req.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		assert.Less(t, rec.Code, http.StatusInternalServerError, "body: %s", rec.Body.String())

		if !bulk && (flagKey == "." || flagKey == "..") {
			// the mux cleans the dot segment before the handler runs and redirects: no body for a POST
			assert.Equal(t, http.StatusTemporaryRedirect, rec.Code)
			assert.Equal(t, path.Clean(target), rec.Header().Get("Location"))

			return
		}

		assert.True(t, json.Valid(rec.Body.Bytes()), "response is not JSON: %s", rec.Body.String())
	})
}
