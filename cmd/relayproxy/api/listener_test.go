package api_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/cmdlog"
	"github.com/blackbirdworks/semaphore/testhelpers"
	"github.com/blackbirdworks/semaphore/testhelpers/slogutil"
)

const (
	acceptFailed    = "accept failed"
	loopbackAddress = "127.0.0.1"
)

var errAcceptFailed = errors.New(acceptFailed)

// failingListener is a listener whose Accept fails at once: the server it is given to cannot serve.
type failingListener struct{}

func (failingListener) Accept() (net.Conn, error) { return nil, errAcceptFailed }

func (failingListener) Close() error { return nil }

func (failingListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

// Test_Listener checks that a listener that cannot be bound, or that fails while serving, is a
// fatal error of the process (as it is on main), whatever the listener, and promptly: the failing server must not
// wait for a shutdown that nobody asks for.
func Test_Listener(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setup       func(t *testing.T, conf *testConf) []api.Option
		wantMessage string
		wantCause   string
		name        string
	}{
		{
			name:        "api accept fails",
			wantMessage: "Error starting relay proxy",
			wantCause:   acceptFailed,
			setup: func(*testing.T, *testConf) []api.Option {
				return []api.Option{api.WithListener(failingListener{})}
			},
		},
		{
			name:        "monitoring accept fails",
			wantMessage: "Error starting monitoring",
			wantCause:   acceptFailed,
			setup: func(t *testing.T, conf *testConf) []api.Option {
				t.Helper()
				ln, port := testhelpers.ListenLocal(t)
				conf.settings.MonitoringPort = port + 1

				return []api.Option{api.WithListener(ln), api.WithMonitoringListener(failingListener{})}
			},
		},
		{
			name:        "api port bound",
			wantMessage: "Error starting relay proxy",
			wantCause:   "address already in use",
			setup: func(t *testing.T, conf *testConf) []api.Option {
				t.Helper()
				_, port := testhelpers.ListenLocal(t)
				conf.settings.Host = loopbackAddress
				conf.settings.Port = port

				return nil
			},
		},
		{
			name:        "monitoring port bound",
			wantMessage: "Error starting monitoring",
			wantCause:   "address already in use",
			setup: func(t *testing.T, conf *testConf) []api.Option {
				t.Helper()
				ln, _ := testhelpers.ListenLocal(t)
				_, monitoringPort := testhelpers.ListenLocal(t)
				conf.settings.Host = loopbackAddress
				conf.settings.MonitoringPort = monitoringPort

				return []api.Option{api.WithListener(ln)}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conf := testConfig(t)
			opts := tt.setup(t, conf)
			flagsetManager := newDefaultFlags(t, nil)
			conf.flags = flagsetManager

			exited := make(chan struct{}, 1)

			opts = append(opts, api.WithExit(func() {
				select {
				case exited <- struct{}{}:
				default:
				}

				runtime.Goexit()
			}))
			logger, logs := slogutil.NewObserver(slog.LevelDebug)

			srv := newTestServer(t, conf, &service.Services{}, logger, opts...)
			go func() { srv.StartWithContext(context.Background()) }()

			t.Cleanup(func() {
				stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				srv.Stop(stopCtx)
			})

			select {
			case <-exited:
			case <-time.After(10 * time.Second):
				require.Fail(t, "the failure of the listener never reached the fatal handler")
			}

			// the fatal handler was reached for the failure that was provoked, not for another one
			var fatal []map[string]any

			for _, entry := range logs.All() {
				if entry.Level == cmdlog.LevelFatal {
					fields := entry.ContextMap()
					fields["message"] = entry.Message
					fatal = append(fatal, fields)
				}
			}

			require.Len(t, fatal, 1)
			assert.Equal(t, tt.wantMessage, fatal[0]["message"])
			assert.Contains(t, fmt.Sprint(fatal[0]["error"]), tt.wantCause)
		})
	}
}
