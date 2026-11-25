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
	"github.com/blackbirdworks/semaphore/testhelpers"
)

const (
	http12700 = "http://127.0.0.1:%d"
)

func TestServerEnableH2C(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		wantProtoMaj int
		enableH2C    bool
		clientH2C    bool
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
			listener, port := testhelpers.ListenLocal(t)
			conf := &config.Config{
				Retrievers: &[]retrieverconf.RetrieverConf{{Kind: file2, Path: testdataFlagConfigYaml}},
				Server: config.Server{
					Mode:      config.ServerModeHTTP,
					Host:      loopbackAddress,
					Port:      port,
					EnableH2C: tt.enableH2C,
				},
			}
			log := newTestLogger(t)
			metrics, err := metric.NewMetrics()
			require.NoError(t, err)

			wsSvc := stream.NewWebsocketService()
			defer wsSvc.Close()

			flagsetMgr, err := service.NewFlagsetManager(conf, log.Slog,
				[]notifier.Notifier{metric.NewPrometheusNotifier(metrics)}, nil, wsSvc)
			require.NoError(t, err)

			srv := newTestServer(t, conf, service.Services{
				MonitoringService: service.NewMonitoring(
					flagsetMgr,
				),
				WebsocketService: wsSvc,
				FlagsetManager:   flagsetMgr,
				Metrics:          metrics,
			}, log.Slog, api.WithListener(listener))
			go func() { srv.StartWithContext(context.Background()) }()

			defer srv.Stop(context.Background())

			baseURL := fmt.Sprintf(http12700, port)
			waitForServer(t, baseURL)

			var protos http.Protocols
			if tt.clientH2C {
				protos.SetUnencryptedHTTP2(true)
			} else {
				protos.SetHTTP1(true)
			}

			client := &http.Client{Transport: &http.Transport{Protocols: &protos}}
			defer client.CloseIdleConnections()

			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, baseURL+health2, http.NoBody)
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
