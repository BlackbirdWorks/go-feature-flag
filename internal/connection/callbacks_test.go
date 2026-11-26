package connection_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

// reentrantHandler is a slog handler that calls back into the factory, as a handler that logs through the
// connection layer would.
type reentrantHandler struct {
	call  func()
	calls atomic.Int32
}

func (*reentrantHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *reentrantHandler) Handle(context.Context, slog.Record) error {
	h.calls.Add(1)
	h.call()

	return nil
}
func (h *reentrantHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *reentrantHandler) WithGroup(string) slog.Handler      { return h }

type observerFunc func(conn, operation, result string)

func (f observerFunc) Observe(conn, operation, result string) { f(conn, operation, result) }

// finishes runs fn and reports whether it returned within the timeout: a deadlock never returns.
func finishes(run func()) bool {
	done := make(chan struct{})

	go func() {
		defer close(done)

		run()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

// TestFactory_NoLockIsHeldAcrossACallback checks, per kind of callback, that the callback can call back into
// the factory (the same connection included) while the factory is building or accepting a spec. It fails by
// timing out if a lock were held across the callback, and by the count if the callback never ran.
func TestFactory_NoLockIsHeldAcrossACallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// setup returns the factory and the action that triggers the callback; calls counts the callbacks.
		setup     func(t *testing.T, calls *atomic.Int32, errs *atomic.Value) (*connection.Factory, func() error)
		name      string
		wantCalls int32
	}{
		{
			name: "logger during a build",
			setup: func(t *testing.T, calls *atomic.Int32, errs *atomic.Value) (*connection.Factory, func() error) {
				t.Helper()

				var fac *connection.Factory

				handler := &reentrantHandler{}
				handler.call = func() {
					calls.Add(1)

					_, err := fac.AWSConfig(context.Background(), "a")
					errs.Store(errBox{err})
					assert.Equal(t, []string{"a"}, fac.Names())
					assert.Equal(t, map[string]int{"a": 1}, fac.Builds())
				}

				fac = connection.New(connection.WithLogger(slog.New(handler)))
				require.NoError(t, fac.Set("a", testS3()))

				return fac, func() error {
					_, err := fac.S3(context.Background(), "a")

					return err
				}
			},
			wantCalls: 1,
		},
		{
			name: "ReadFile during a Set",
			setup: func(t *testing.T, calls *atomic.Int32, errs *atomic.Value) (*connection.Factory, func() error) {
				t.Helper()

				authority := newTestCA(t, "CA")

				var fac *connection.Factory

				fac = connection.New(connection.WithReadFile(func(string) ([]byte, error) {
					calls.Add(1)
					errs.Store(errBox{nil})
					assert.Equal(t, []string{"a"}, fac.Names())

					return authority.pem, nil
				}))
				require.NoError(t, fac.Set("a", testS3()))

				return fac, func() error {
					return fac.Set("a", other(func(s *connection.S3Spec) { s.CABundleFile = testCAPath }))
				}
			},
			wantCalls: 1,
		},
		{
			name: "observer during a request",
			setup: func(t *testing.T, calls *atomic.Int32, errs *atomic.Value) (*connection.Factory, func() error) {
				t.Helper()

				var fac *connection.Factory

				obs := observerFunc(func(string, string, string) {
					calls.Add(1)

					_, err := fac.AWSConfig(context.Background(), "a")
					errs.Store(errBox{err})
				})
				fac = connection.New(connection.WithObserver(obs),
					connection.WithRoundTripper(newScripted(step{status: 200})))
				require.NoError(t, fac.Set("a", testS3()))

				return fac, func() error {
					client, err := fac.S3(context.Background(), "a")
					if err != nil {
						return err
					}

					return headObject(t, client)
				}
			},
			wantCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				calls  atomic.Int32
				errs   atomic.Value
				actErr error
			)

			_, action := tt.setup(t, &calls, &errs)

			require.True(t, finishes(func() { actErr = action() }), "a lock was held while the callback ran")
			require.NoError(t, actErr)
			assert.Equal(t, tt.wantCalls, calls.Load())

			if box, ok := errs.Load().(errBox); ok {
				require.NoError(t, box.err, "the callback could not use the factory")
			}
		})
	}
}

// errBox lets an [atomic.Value] hold a nil error.
type errBox struct{ err error }
