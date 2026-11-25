package api

import (
	"log/slog"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

type serverOptions struct {
	listener           net.Listener
	monitoringListener net.Listener
	globalTelemetry    bool
}

func TestNew_Options(t *testing.T) {
	t.Parallel()

	apiListener := &net.TCPListener{}
	monitoringListener := &net.TCPListener{}

	tests := []struct {
		want serverOptions
		name string
		opts []Option
	}{
		{name: "no option", opts: nil, want: serverOptions{}},
		{
			name: "global telemetry (only installed when the server starts)",
			opts: []Option{WithGlobalTelemetry()},
			want: serverOptions{globalTelemetry: true},
		},
		{
			name: "listener of the API server",
			opts: []Option{WithListener(apiListener)},
			want: serverOptions{listener: apiListener},
		},
		{
			name: "listener of the monitoring server",
			opts: []Option{WithMonitoringListener(monitoringListener)},
			want: serverOptions{monitoringListener: monitoringListener},
		},
		{
			name: "every option",
			opts: []Option{
				WithGlobalTelemetry(),
				WithListener(apiListener),
				WithMonitoringListener(monitoringListener),
			},
			want: serverOptions{globalTelemetry: true, listener: apiListener, monitoringListener: monitoringListener},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := New(&config.Config{}, service.Services{}, slog.New(slog.DiscardHandler), tt.opts...)

			assert.Equal(t, tt.want.globalTelemetry, s.installGlobalTelemetry)
			assertSameListener(t, tt.want.listener, s.listener)
			assertSameListener(t, tt.want.monitoringListener, s.monitoringListener)
		})
	}
}

// assertSameListener checks that got is the very listener that is wanted (and not another one with the same
// content), or that both are nil.
func assertSameListener(t *testing.T, want, got net.Listener) {
	t.Helper()

	if want == nil {
		assert.Nil(t, got)

		return
	}

	assert.Same(t, want, got)
}
