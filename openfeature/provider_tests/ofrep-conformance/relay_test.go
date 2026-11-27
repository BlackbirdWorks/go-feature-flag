package ofrepconformance_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/cmdhelpers/cmdlog"
)

const (
	relayReadyTimeout = 15 * time.Second
	relayPollInterval = 20 * time.Millisecond
	roundsBuffer      = 16
)

func noEnv(string) (string, bool) { return "", false }

// startRelay starts an in-process Semaphore relay proxy from a bootstrap YAML file in
// openfeature/provider_tests/ and returns its loopback base URL.
func startRelay(t *testing.T, bootstrapFile string) string {
	t.Helper()

	cfgPath := filepath.Join("..", bootstrapFile)
	load := bootstrap.LoadOptions{ReadFile: os.ReadFile, LookupEnv: noEnv}

	cfg, err := bootstrap.Load(&bootstrap.Overrides{Config: cfgPath}, load)
	require.NoError(t, err)

	conn := cfg.Connections["control"]
	require.NotNil(t, conn.File)
	conn.File.Path = filepath.Join("..", "store")

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	require.True(t, ok, "expected *net.TCPAddr listener address")

	metrics, err := metric.NewMetrics()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	streams := corehttp.NewStreams(ctx)
	rounds := make(chan flagruntime.RoundResult, roundsBuffer)

	core, err := relaycore.New(ctx, cfg, &relaycore.Deps{
		Registerer: metrics.Registry,
		Broadcast:  streams.Broadcast,
		Secrets:    load,
		Hooks: flagruntime.Hooks{AfterRound: func(res flagruntime.RoundResult) {
			select {
			case rounds <- res:
			default:
			}
		}},
	})
	require.NoError(t, err)

	settings, err := corehttp.SettingsFrom(cfg, "conformance-test")
	require.NoError(t, err)

	settings.Host = "127.0.0.1"
	settings.Port = tcpAddr.Port

	logger := cmdlog.NewDefault(cmdlog.WithWriter(io.Discard)).Slog
	ready := corehttp.NewReadiness(core)

	server, err := corehttp.New(core, &corehttp.Config{
		Settings:  &settings,
		Logger:    logger,
		Streams:   streams,
		Metrics:   metrics,
		Readiness: ready,
	}, api.WithListener(listener))
	require.NoError(t, err)

	var loops sync.WaitGroup
	loops.Go(func() { _ = core.Run(ctx) })
	loops.Go(func() { server.StartWithContext(ctx) })

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), relayReadyTimeout)
		defer stopCancel()

		_ = streams.Close(stopCtx)
		server.Stop(stopCtx)
		_ = core.Shutdown(stopCtx)

		cancel()
		loops.Wait()
	})

	waitFirstGeneration(t, rounds)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", tcpAddr.Port)
	waitReadyz(t, baseURL)

	return baseURL
}

func waitFirstGeneration(t *testing.T, rounds <-chan flagruntime.RoundResult) {
	t.Helper()

	timer := time.NewTimer(relayReadyTimeout)
	defer timer.Stop()

	for {
		select {
		case res := <-rounds:
			if res.Generation != 1 {
				continue
			}

			require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, "relay rejected test flag store")

			return
		case <-timer.C:
			require.FailNow(t, "timed out waiting for relay generation 1 to apply")
		}
	}
}

func waitReadyz(t *testing.T, baseURL string) {
	t.Helper()

	client := &http.Client{Timeout: relayReadyTimeout}
	deadline := time.Now().Add(relayReadyTimeout)

	for {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/readyz", http.NoBody)
		require.NoError(t, err)

		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				return
			}
		}

		require.False(t, time.Now().After(deadline), "relay /readyz did not become ready within %s", relayReadyTimeout)

		timer := time.NewTimer(relayPollInterval)
		select {
		case <-t.Context().Done():
			timer.Stop()
			require.FailNow(t, "context cancelled while waiting for relay /readyz")
		case <-timer.C:
		}
	}
}
