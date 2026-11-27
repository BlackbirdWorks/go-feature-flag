package relayapp

import (
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
)

// A real SIGTERM, as the signal context of main.go sees it, stops the relay: exit status 0 and the steps logged in
// order. The listener is open from the start, so no port is chosen and released.
func TestSIGTERMShutsTheRelayDown(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	(&store{t: t, dir: dir}).publish(1)

	cfg := filepath.Join(t.TempDir(), "bootstrap.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte(bootstrapDoc(dir, "")), 0o600))

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = listener.Close() }) // already closed by the relay when the test passes

	address := listener.Addr().String()

	ctx, stop := signal.NotifyContext(t.Context(), syscall.SIGTERM)
	defer stop()

	var stderr syncBuffer

	done := make(chan int, 1)

	go func() {
		done <- mainWith(ctx, []string{"--config", cfg}, &syncBuffer{}, &stderr, noEnv, Build{Version: "v1"},
			[]api.Option{api.WithListener(listener)})
	}()

	require.Eventually(t, func() bool {
		req, reqErr := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+address+readyPath, http.NoBody)
		if reqErr != nil {
			return false
		}

		resp, doErr := http.DefaultClient.Do(req)
		if doErr != nil {
			return false
		}

		_ = resp.Body.Close()

		return resp.StatusCode == http.StatusOK
	}, waitBound, 20*time.Millisecond, "the relay never became ready")

	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))

	select {
	case code := <-done:
		assert.Zero(t, code)
	case <-time.After(waitBound):
		require.FailNow(t, "the relay did not exit on SIGTERM", stderr.String())
	}

	log := stderr.String()
	last := -1

	for _, step := range []string{
		"shutdown: readiness off", "shutdown: change streams closed", "shutdown: API server stopped",
		"shutdown: core stopped",
	} {
		at := strings.Index(log, step)
		require.Positive(t, at, "%q is logged", step)
		assert.Greater(t, at, last, "%q follows the previous step", step)

		last = at
	}

	assert.NotContains(t, log, "stopped hard")
}
