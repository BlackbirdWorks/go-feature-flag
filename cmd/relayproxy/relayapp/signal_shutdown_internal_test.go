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
		failStart bool
	}{
		{
			name: "SIGTERM stops the relay in order", signals: []syscall.Signal{syscall.SIGTERM},
			wantLog: []string{
				"shutdown: readiness off", logStreams, "shutdown: API server stopped",
				logCore,
			},
			wantNoLog: "stopped hard",
		},
		{
			name: "a second SIGINT during the drain delay stops hard", extra: drainExtra,
			signals:  []syscall.Signal{syscall.SIGTERM, syscall.SIGINT},
			wantCode: 1,
			wantLog:  []string{"shutdown: stopped hard"}, // the signal may come before or during the drain delay
		},
		{
			// the first signal after a failure is the operator's stop request: cleanup goes on, nothing is cut
			name: "one SIGTERM after a failed start is not a hard stop", failStart: true, extra: drainExtra,
			signals: []syscall.Signal{syscall.SIGTERM}, wantCode: 1,
			wantLog:   []string{logStreams, logCore},
			wantNoLog: "stopped hard",
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
			release := make(chan struct{})
			done := make(chan int, 1)

			go func() {
				done <- mainWith(ctx, []string{"--config", cfg}, &syncBuffer{}, &stderr, noEnv, Build{Version: testVersion},
					func(opts *runOptions) {
						opts.ServerOptions = []api.Option{api.WithListener(listener)}
						if tt.failStart {
							opts.ServerOptions = []api.Option{
								api.WithListenFunc(func(context.Context, string, string) (net.Listener, error) {
									return nil, errPortInUse
								}),
							}
						}

						// the drain delay of the real timer is long: only the second signal ends it
						opts.OnDraining = func(context.Context) {
							close(draining)

							if tt.failStart {
								<-release // the cleanup after the failure is held until the signal arrived
							}
						}
					})
			}()

			if tt.failStart {
				// registered before the wait: the held hook is released whatever happens next
				t.Cleanup(func() {
					select {
					case <-release:
					default:
						close(release)
					}
				})

				awaitSignal(t, draining, "the cleanup after the failure did not start", &stderr)
			} else {
				require.Eventually(t, func() bool { return ready(t.Context(), address) }, waitBound,
					20*time.Millisecond, "the relay never became ready")
			}

			require.NoError(t, syscall.Kill(os.Getpid(), tt.signals[0]))

			if tt.failStart {
				// the run context ended: the signal was seen by the first watcher only
				select {
				case <-ctx.Done():
				case <-time.After(waitBound):
					require.FailNow(t, "the run context did not end on the signal", stderr.String())
				}

				close(release)
			}

			if len(tt.signals) > 1 {
				// the shutdown context watches signals before the hook runs
				awaitSignal(t, draining, "the shutdown did not start", &stderr)

				require.NoError(t, syscall.Kill(os.Getpid(), tt.signals[1]))
			}

			select {
			case code := <-done:
				assert.Equal(t, tt.wantCode, code)
			case <-time.After(waitBound):
				require.FailNow(t, "the relay did not exit", stderr.String())
			}

			assertSteps(t, stderr.String(), tt.wantLog)

			if tt.wantNoLog != "" {
				assert.NotContains(t, stderr.String(), tt.wantNoLog)
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

// awaitSignal waits for the channel, within the failure bound.
func awaitSignal(t *testing.T, signalled <-chan struct{}, failure string, stderr *syncBuffer) {
	t.Helper()

	select {
	case <-signalled:
	case <-time.After(waitBound):
		require.FailNow(t, failure, stderr.String())
	}
}

// assertSteps checks that every step is in the log, in the order given.
func assertSteps(t *testing.T, log string, steps []string) {
	t.Helper()

	last := -1

	for _, step := range steps {
		at := strings.Index(log, step)
		require.Positive(t, at, "%q is logged", step)
		assert.Greater(t, at, last, "%q follows the previous step", step)

		last = at
	}
}
