package ofrep_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
)

// FuzzOFREPEvaluate checks that any request body sent to the OFREP endpoints
// gets a JSON answer that is not a server error, and never panics the handler.
func FuzzOFREPEvaluate(f *testing.F) {
	requests, err := filepath.Glob(testdataDir + "/ofrep/*.json")
	require.NoError(f, err)

	for _, file := range requests {
		content, readFileErr := os.ReadFile(file)
		require.NoError(f, readFileErr)
		f.Add(numberFlag, string(content), false)
		f.Add("", string(content), true)
	}

	flagsetManager, err := service.NewFlagsetManager(&config.Config{
		PollingInterval: 10000,
		FileFormat:      yaml,
		Retrievers: &[]retrieverconf.RetrieverConf{
			{Kind: retrieverconf.FileRetriever, Path: configFlagsLocation},
		},
	}, slog.New(slog.DiscardHandler), nil, nil, nil)
	require.NoError(f, err)
	f.Cleanup(flagsetManager.Close)

	ctrl := ofrep.NewOFREPEvaluate(flagsetManager, metric.Metrics{}, config.OfrepEventStream{})
	mux := http.NewServeMux()
	mux.Handle(pOSTOfrepV1, helper.Handle(ctrl.Evaluate, false))
	mux.Handle("POST /ofrep/v1/evaluate/flags", helper.Handle(ctrl.BulkEvaluate, false))

	f.Fuzz(func(t *testing.T, flagKey, body string, bulk bool) {
		target := ofrepV1Evaluate2 + url.PathEscape(flagKey)
		if bulk {
			target = ofrepV1Evaluate
		}

		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		req.Header.Set(contentType, applicationJson)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		assert.Less(t, rec.Code, http.StatusInternalServerError, "body: %s", rec.Body.String())
		assert.True(t, json.Valid(rec.Body.Bytes()), "response is not JSON: %s", rec.Body.String())
	})
}
