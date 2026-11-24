package api

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
)

type serverOptions struct {
	globalTelemetry    bool
	listener           net.Listener
	monitoringListener net.Listener
}

func TestNew_Options(t *testing.T) {
	t.Parallel()

	apiListener := &net.TCPListener{}
	monitoringListener := &net.TCPListener{}

	tests := []struct {
		name string
		opts []Option
		want serverOptions
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
			opts: []Option{WithGlobalTelemetry(), WithListener(apiListener), WithMonitoringListener(monitoringListener)},
			want: serverOptions{globalTelemetry: true, listener: apiListener, monitoringListener: monitoringListener},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := New(&config.Config{}, service.Services{}, zap.NewNop(), tt.opts...)

			assert.Equal(t, tt.want, serverOptions{
				globalTelemetry:    s.installGlobalTelemetry,
				listener:           s.listener,
				monitoringListener: s.monitoringListener,
			})
		})
	}
}
