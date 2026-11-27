package corehttp_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/opentelemetry"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
)

const roleAPI = "api"

func listeners(l ...bootstrap.Listener) bootstrap.Config {
	return bootstrap.Config{Server: bootstrap.Server{Listeners: l}}
}

func TestSettingsFrom(t *testing.T) {
	t.Parallel()

	withTelemetry := listeners(bootstrap.Listener{Name: roleAPI, Address: "127.0.0.1:8080", Role: bootstrap.RoleAPI,
		Protocols: []string{bootstrap.ProtocolHTTP1, bootstrap.ProtocolH2C}})
	withTelemetry.Observability.Metrics.LegacyNames = true
	withTelemetry.Observability.Tracing = bootstrap.Tracing{OTLPEndpoint: "collector:4317", ServiceName: "svc",
		Sampler: "always_on"}
	withTelemetry.Evaluation.DisableSemaphoreMetadata = true

	tests := []struct {
		name    string
		cfg     bootstrap.Config
		want    api.Settings
		wantErr bool
	}{
		{
			name: "one api listener",
			cfg:  listeners(bootstrap.Listener{Name: roleAPI, Address: ":1031", Role: bootstrap.RoleAPI}),
			want: api.Settings{Version: "v", Mode: api.ServerModeHTTP, Port: 1031,
				Telemetry: opentelemetry.Settings{Version: "v"}},
		},
		{
			name: "api with h2c, legacy metrics, tracing and metadata",
			cfg:  withTelemetry,
			want: api.Settings{Version: "v", Mode: api.ServerModeHTTP, Host: "127.0.0.1", Port: 8080, EnableH2C: true,
				ExposeLegacyMetrics: true, DisableSemaphoreMetadata: true,
				Telemetry: opentelemetry.Settings{Version: "v", OTLPEndpoint: "collector:4317", ServiceName: "svc",
					Sampler: "always_on"}},
		},
		{
			name: "api and ops on one host",
			cfg: listeners(
				bootstrap.Listener{Name: roleAPI, Address: "0.0.0.0:1", Role: bootstrap.RoleAPI},
				bootstrap.Listener{Name: "ops", Address: "0.0.0.0:2", Role: bootstrap.RoleOps}),
			want: api.Settings{Version: "v", Mode: api.ServerModeHTTP, Host: "0.0.0.0", Port: 1, MonitoringPort: 2,
				Telemetry: opentelemetry.Settings{Version: "v"}},
		},
		{
			name: "ops on another host",
			cfg: listeners(
				bootstrap.Listener{Name: roleAPI, Address: "0.0.0.0:1", Role: bootstrap.RoleAPI},
				bootstrap.Listener{Name: "ops", Address: "127.0.0.1:2", Role: bootstrap.RoleOps}),
			wantErr: true,
		},
		{
			name: "two api listeners",
			cfg: listeners(
				bootstrap.Listener{Name: "a", Address: ":1", Role: bootstrap.RoleAPI},
				bootstrap.Listener{Name: "b", Address: ":2", Role: bootstrap.RoleAPI}),
			wantErr: true,
		},
		{
			name: "two ops listeners",
			cfg: listeners(
				bootstrap.Listener{Name: "a", Address: ":1", Role: bootstrap.RoleOps},
				bootstrap.Listener{Name: "b", Address: ":2", Role: bootstrap.RoleOps}),
			wantErr: true,
		},
		{name: "a port that is not a number", cfg: listeners(bootstrap.Listener{Address: ":http"}), wantErr: true},
		{name: "port zero", cfg: listeners(bootstrap.Listener{Address: ":0"}), wantErr: true},
		{name: "no port", cfg: listeners(bootstrap.Listener{Address: "localhost"}), wantErr: true},
		{
			name:    "a unix socket",
			cfg:     bootstrap.Config{Server: bootstrap.Server{UnixSocketPath: "/run/s.sock"}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := corehttp.SettingsFrom(&tt.cfg, "v")
			if tt.wantErr {
				require.ErrorIs(t, err, corehttp.ErrListeners)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRefreshIsNotSupported(t *testing.T) {
	t.Parallel()

	rig := newV2(t)

	status, _, body := do(t, http.MethodPost, rig.url+pathRefresh, "", keyHeader(adminKey))

	assert.Equal(t, http.StatusNotImplemented, status)
	assert.JSONEq(t, `{"message":"refresh on request is not supported: the relay polls the control plane manifest"}`,
		string(body))
}

// The settings of the file reach the HTTP layer: the profiler, the advertised event stream and the validate limits.
func TestSettingsFromServerSettings(t *testing.T) {
	t.Parallel()

	const (
		kib        = 1 << 10
		streamBase = "https://flags.example.com"
	)

	jsonLimit, yamlLimit := bootstrap.ByteSize(2<<20), bootstrap.ByteSize(100*kib)

	apiOnly := bootstrap.Listener{Name: roleAPI, Address: ":1031", Role: bootstrap.RoleAPI}
	tests := []struct {
		name       string
		server     bootstrap.Server
		wantPprof  bool
		wantStream bool
		wantLimits controller.ValidateFlagsLimits
		wantErr    bool
	}{
		{name: "nothing set", server: bootstrap.Server{Listeners: []bootstrap.Listener{apiOnly}}},
		{
			name: "pprof on", wantPprof: true,
			server: bootstrap.Server{
				Listeners: []bootstrap.Listener{apiOnly}, Ops: bootstrap.Ops{Pprof: bootstrap.Pprof{Enabled: true}},
			},
		},
		{
			name: "event stream advertised", wantStream: true,
			server: bootstrap.Server{
				Listeners:        []bootstrap.Listener{apiOnly},
				OFREPEventStream: bootstrap.OFREPEventStream{BaseURL: streamBase, InactivityDelaySec: 30},
			},
		},
		{
			name: "event stream with a URL that is not valid", wantErr: true,
			server: bootstrap.Server{
				Listeners:        []bootstrap.Listener{apiOnly},
				OFREPEventStream: bootstrap.OFREPEventStream{BaseURL: "ftp://x"},
			},
		},
		{
			name: "limits set", wantLimits: controller.ValidateFlagsLimits{MaxJSONBodySize: 2 << 20, MaxYAMLBodySize: 100 * kib},
			server: bootstrap.Server{
				Listeners:     []bootstrap.Listener{apiOnly},
				ValidateFlags: bootstrap.ValidateFlags{MaxJSONBodySize: &jsonLimit, MaxYAMLBodySize: &yamlLimit},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := corehttp.SettingsFrom(&bootstrap.Config{Server: tt.server}, "v")
			if tt.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantPprof, got.Pprof)
			assert.Equal(t, tt.wantStream, got.OfrepEventStream.IsEnabled())
			assert.Equal(t, tt.wantLimits, got.ValidateFlags)
		})
	}
}
