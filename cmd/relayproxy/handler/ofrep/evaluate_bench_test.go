package ofrep_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/internal/protocol"
)

// BenchmarkEvaluate measures one OFREP single-flag evaluation through the handler, request binding and response
// writing included. It guards the evaluation hot path: allocations per operation must not increase.
func BenchmarkEvaluate(b *testing.B) {
	conf := &config.Config{
		PollingInterval: 10000,
		FileFormat:      protocol.FormatYAML,
		Retrievers: &[]retrieverconf.RetrieverConf{
			{Kind: retrieverconf.FileRetriever, Path: configFlagsLocation},
		},
	}

	flagsetManager, err := service.NewFlagsetManager(conf, slog.New(slog.DiscardHandler), nil, nil, nil)
	require.NoError(b, err)

	defer flagsetManager.Close()

	body, err := os.ReadFile(testdataDir + ofrepValidRequestJSON)
	require.NoError(b, err)

	ctrl := ofrep.NewOFREPEvaluate(flagsetManager, &metric.Metrics{}, ofrep.EventStream{})
	payload := string(body)

	b.ReportAllocs()

	for b.Loop() {
		req := httptest.NewRequestWithContext(b.Context(), http.MethodPost, ofrepEvaluatePrefix+numberFlag,
			strings.NewReader(payload))
		req.SetPathValue("flagKey", numberFlag)
		req.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)

		rec := httptest.NewRecorder()
		require.NoError(b, ctrl.Evaluate(rec, req))
		require.Equal(b, http.StatusOK, rec.Code)
	}
}
