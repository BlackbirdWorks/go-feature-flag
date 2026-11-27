package relayapp

import (
	"context"
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

// Real signals, as the contexts of main.go see them. A signal reaches every context of the process that watches
// it, so the rows run one after the other and are not parallel. The listener is open from the start: no port is
// chosen and released.
//
//nolint:paralleltest // a real signal reaches every watching context of the process: the rows must not overlap
func TestRealSignals(t *testing.T) {
	tests := []struct {
		name      string
		extra     string
		wantNoLog string
		wantLog   []string
		signals   []syscall.Signal
		wantCode  int
	}{
		{
			name: "SIGTERM stops the relay in order", signals: []syscall.Signal{syscall.SIGTERM},
			wantLog: []string{
				"shutdown: readiness off", "shutdown: change streams closed", "shutdown: API server stopped",
				"shutdown: core stopped",
			},
			wantNoLog: "stopped hard",
		},
		{
			name: "a second SIGINT during the drain delay stops hard", extra: drainExtra,
			signals:  []syscall.Signal{syscall.SIGTERM, syscall.SIGINT},
			wantCode: 1,
			wantLog:  []string{"shutdown: the drain delay was cut short", "shutdown: stopped hard"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			(&store{t: t, dir: dir}).publish(1)

			cfg := filepath.Join(t.TempDir(), "bootstrap.yaml")
			require.NoError(t, os.WriteFile(cfg, []byte(bootstrapDoc(dir, tt.extra)), 0o600))

			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			require.NoError(t, err)

			t.Cleanup(func() { _ = listener.Close() }) // already closed by the relay when the test passes

			address := listener.Addr().String()

			ctx, stop := signal.NotifyContext(t.Context(), syscall.SIGTERM)
			defer stop()

			var stderr syncBuffer

			draining := make(chan struct{})
			done := make(chan int, 1)

			go func() {
				done <- mainWith(ctx, []string{"--config", cfg}, &syncBuffer{}, &stderr, noEnv, Build{Version: testVersion},
					func(opts *runOptions) {
						opts.ServerOptions = []api.Option{api.WithListener(listener)}
						// the drain delay of the real timer is long: only the second signal ends it
						opts.OnDraining = func(context.Context) { close(draining) }
					})
			}()

			require.Eventually(t, func() bool { return ready(t.Context(), address) }, waitBound, 20*time.Millisecond,
				"the relay never became ready")

			require.NoError(t, syscall.Kill(os.Getpid(), tt.signals[0]))

			if len(tt.signals) > 1 {
				// the shutdown context watches signals before the hook runs
				select {
				case <-draining:
				case <-time.After(waitBound):
					require.FailNow(t, "the shutdown did not start", stderr.String())
				}

				require.NoError(t, syscall.Kill(os.Getpid(), tt.signals[1]))
			}

			select {
			case code := <-done:
				assert.Equal(t, tt.wantCode, code)
			case <-time.After(waitBound):
				require.FailNow(t, "the relay did not exit", stderr.String())
			}

			log := stderr.String()
			last := -1

			for _, step := range tt.wantLog {
				at := strings.Index(log, step)
				require.Positive(t, at, "%q is logged", step)
				assert.Greater(t, at, last, "%q follows the previous step", step)

				last = at
			}

			if tt.wantNoLog != "" {
				assert.NotContains(t, log, tt.wantNoLog)
			}
		})
	}
}

// ready tells whether /readyz of the address answers 200.
func ready(ctx context.Context, address string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+readyPath, http.NoBody)
	if err != nil {
		return false
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}

	_ = resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}
