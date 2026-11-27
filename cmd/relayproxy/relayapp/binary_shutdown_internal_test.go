package relayapp

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The real binary exits 0 on SIGTERM and logs the steps of the shutdown in order.
func TestBinaryShutsDownOnSIGTERM(t *testing.T) {
	t.Parallel()

	bin := filepath.Join(t.TempDir(), "semaphore")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "..")
	out, err := build.CombinedOutput()
	require.NoError(t, err, string(out))

	dir := t.TempDir()
	(&store{t: t, dir: dir}).publish(1)

	cfg := filepath.Join(t.TempDir(), "bootstrap.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte(bootstrapDoc(dir, "")), 0o600))

	// the binary binds the address itself: the port is the one that was free a moment ago
	probe, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := probe.Addr().String()
	require.NoError(t, probe.Close())

	var stderr syncBuffer

	cmd := exec.CommandContext(context.WithoutCancel(t.Context()), bin, "--config", cfg, "--listen", address)
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())

	exited := make(chan error, 1)

	go func() {
		exited <- cmd.Wait()

		close(exited) // the cleanup reads again after the test read the status
	}()

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		waitErr := <-exited

		_ = waitErr // killed or already read: the status does not matter here
	})

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
	}, waitBound, 50*time.Millisecond, "the relay never became ready: %s", stderr.String())

	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))

	select {
	case waitErr := <-exited:
		require.NoError(t, waitErr, "exit status 0")
	case <-time.After(waitBound):
		require.FailNow(t, "the binary did not exit on SIGTERM", stderr.String())
	}

	log := stderr.String()
	last := -1

	for _, step := range []string{
		"shutdown: readiness off", "shutdown: change streams closed", "shutdown: API server stopped",
		"shutdown: core stopped",
	} {
		at := strings.Index(log, step)
		require.Positive(t, at, "%q is logged: %s", step, log)
		assert.Greater(t, at, last, "%q follows the previous step", step)

		last = at
	}

	assert.NotContains(t, log, "stopped hard")
	assert.Zero(t, cmd.ProcessState.ExitCode())
}
