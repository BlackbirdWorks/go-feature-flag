package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testutils"
)

func TestServerEnableH2C(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		enableH2C    bool
		clientH2C    bool
		wantProtoMaj int
		wantErr      bool
	}{
		{name: "default server, HTTP/1.1 client", enableH2C: false, clientH2C: false, wantProtoMaj: 1},
		{name: "h2c server, HTTP/1.1 client still works", enableH2C: true, clientH2C: false, wantProtoMaj: 1},
		{name: "h2c server, h2c client", enableH2C: true, clientH2C: true, wantProtoMaj: 2},
		{name: "default server refuses a prior-knowledge h2c client", enableH2C: false, clientH2C: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			port := testutils.GetFreePort(t)
			conf := &config.Config{
				Retrievers: &[]retrieverconf.RetrieverConf{{Kind: "file", Path: "../../../testdata/flag-config.yaml"}},
				Server: config.Server{
					Mode:      config.ServerModeHTTP,
					Host:      "127.0.0.1",
					Port:      port,
					EnableH2C: tt.enableH2C,
				},
			}
			log := newTestLogger(t)
			metrics, err := metric.NewMetrics()
			require.NoError(t, err)
			ws := stream.NewWebsocketService()
			defer ws.Close()
			fm, err := service.NewFlagsetManager(conf, log.ZapLogger,
				[]notifier.Notifier{metric.NewPrometheusNotifier(metrics)}, nil, ws)
			require.NoError(t, err)
			s := api.New(conf, service.Services{
				MonitoringService: service.NewMonitoring(
					fm,
				),
				WebsocketService: ws,
				FlagsetManager:   fm,
				Metrics:          metrics,
			}, log.ZapLogger)
			go func() { s.StartWithContext(context.Background()) }()
			defer s.Stop(context.Background())

			baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
			waitForServer(t, baseURL)

			var protos http.Protocols
			if tt.clientH2C {
				protos.SetUnencryptedHTTP2(true)
			} else {
				protos.SetHTTP1(true)
			}
			client := &http.Client{Transport: &http.Transport{Protocols: &protos}}
			defer client.CloseIdleConnections()

			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, baseURL+"/health", nil)
			require.NoError(t, err)
			resp, err := client.Do(req)
			if tt.wantErr {
				if err == nil {
					_ = resp.Body.Close()
				}
				assert.Error(t, err)

				return
			}
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, tt.wantProtoMaj, resp.ProtoMajor)
		})
	}
}
