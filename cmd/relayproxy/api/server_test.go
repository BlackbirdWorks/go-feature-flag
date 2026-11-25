package api_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	promdto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protodelim"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testutils"
)

const (
	adminKey                         = "admin-key"
	applicationJSON                  = "application/json"
	bearerTest                       = "Bearer test"
	contextTargetingKeySomeKey       = `{ "context":{"targetingKey":"some-key"}}`
	contextTargetingKeySomeKey2      = `{"context":{"targetingKey":"some-key"}}`
	file2                            = "file"
	httpLocalhostD                   = "http://localhost:%d"
	httpUnixHealth                   = "http://unix/health"
	httpUnixOfrepV                   = "http://unix/ofrep/v1/evaluate/flags/test-flag"
	impossibleToInitializePrometheus = "impossible to initialize prometheus metrics"
	info                             = "/info"
	metrics2                         = "/metrics"
	pOST                             = "POST"
	tcp2                             = "tcp"
	test2                            = "test"
	testdataFlagConfigYaml           = "../../../testdata/flag-config.yaml"
	unix                             = "unix"
	unixSocketFileShould             = "Unix socket file should exist"
	unixSocketFileWas                = "unix socket file was not created in time"
	xGofeatureflagVersion            = "X-Gofeatureflag-Version"
)

// newTestLogger returns a logger that writes to stderr. server.go reports a failed bind with a fatal log,
// and its default reaction is [os.Exit](1): fired from the background goroutine that runs StartWithContext, it
// takes the test binary down and every remaining test in the package is silently skipped. The servers of these
// tests are built with newTestServer, whose exit function fails the test and ends the server goroutine; the
// fatal error is still logged.
func newTestLogger(t *testing.T) *cmdlog.Logger {
	t.Helper()

	return cmdlog.NewDefault()
}

// newTestServer builds a server whose fatal errors (a failed bind, ...) fail the test and end the goroutine of
// the server, instead of calling [os.Exit], which would take the whole test binary down and silently skip the
// remaining tests. Every test server is built with it.
func newTestServer(
	t *testing.T, conf *config.Config, services service.Services, logger *slog.Logger, opts ...api.Option,
) api.Server {
	t.Helper()

	exit := func() {
		select {
		case <-t.Context().Done():
			// the test is over, nobody is left to fail.
		default:
			assert.Fail(t, "the server exited with a fatal error", "see the log of the server")
		}

		runtime.Goexit()
	}

	return api.New(conf, services, logger, append([]api.Option{api.WithExit(exit)}, opts...)...)
}

// waitForServer blocks until the server accepts connections on baseURL. Binding a listener is
// asynchronous, so a fixed sleep is a coin flip: 10ms is below the Windows timer granularity and
// a loaded runner needs far more than that.
func waitForServer(t *testing.T, baseURL string) {
	t.Helper()
	require.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+health2, http.NoBody)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}

		_ = resp.Body.Close()

		return true
	}, 10*time.Second, 20*time.Millisecond, "server never became reachable on %s", baseURL)
}

func Test_Starting_RelayProxy_with_monitoring_on_same_port(t *testing.T) {
	t.Parallel()

	listener, port := testutils.ListenLocal(t)
	proxyConf := &config.Config{
		Retrievers: &[]retrieverconf.RetrieverConf{
			{
				Kind: file2,
				Path: testdataFlagConfigYaml,
			},
		},
		Server: config.Server{
			Mode: config.ServerModeHTTP,
			Port: port,
		},
	}
	log := newTestLogger(t)

	metricsV2, err := metric.NewMetrics()
	if err != nil {
		log.Slog.LogAttrs(
			context.Background(),
			slog.LevelError,
			impossibleToInitializePrometheus,
			cmdlog.Err(err),
		)
	}

	wsService := stream.NewWebsocketService()
	defer wsService.Close() // close all the open connections

	prometheusNotifier := metric.NewPrometheusNotifier(metricsV2)
	flagsetManager, err := service.NewFlagsetManager(proxyConf, log.Slog, []notifier.Notifier{
		prometheusNotifier,
	}, nil, wsService)
	require.NoError(t, err)

	services := service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  wsService,
		FlagsetManager:    flagsetManager,
		Metrics:           metricsV2,
	}

	s := newTestServer(t, proxyConf, services, log.Slog, api.WithListener(listener))
	go func() { s.StartWithContext(context.Background()) }()

	defer s.Stop(context.Background())

	baseURL := fmt.Sprintf(httpLocalhostD, port)
	waitForServer(t, baseURL)

	req2, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+health2, http.NoBody)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)

	defer func() { _ = response.Body.Close() }()

	assert.Equal(t, http.StatusOK, response.StatusCode)

	req3, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+metrics2, http.NoBody)
	require.NoError(t, err)
	responseM, err := http.DefaultClient.Do(req3)
	require.NoError(t, err)

	defer func() { _ = responseM.Body.Close() }()

	assert.Equal(t, http.StatusOK, responseM.StatusCode)

	req4, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+info, http.NoBody)
	require.NoError(t, err)
	responseI, err := http.DefaultClient.Do(req4)
	require.NoError(t, err)

	defer func() { _ = responseI.Body.Close() }()

	assert.Equal(t, http.StatusOK, responseI.StatusCode)
}

func Test_Starting_RelayProxy_with_monitoring_on_different_port(t *testing.T) {
	t.Parallel()

	listener, port := testutils.ListenLocal(t)
	monitoringLn, monitoringPort := testutils.ListenLocal(t)
	proxyConf := &config.Config{
		Retrievers: &[]retrieverconf.RetrieverConf{
			{
				Kind: file2,
				Path: testdataFlagConfigYaml,
			},
		},
		Server: config.Server{
			Mode:           config.ServerModeHTTP,
			Port:           port,
			MonitoringPort: monitoringPort,
		},
	}
	log := newTestLogger(t)

	metricsV2, err := metric.NewMetrics()
	if err != nil {
		log.Slog.LogAttrs(
			context.Background(),
			slog.LevelError,
			impossibleToInitializePrometheus,
			cmdlog.Err(err),
		)
	}

	wsService := stream.NewWebsocketService()
	defer wsService.Close() // close all the open connections

	prometheusNotifier := metric.NewPrometheusNotifier(metricsV2)
	flagsetManager, err := service.NewFlagsetManager(proxyConf, log.Slog, []notifier.Notifier{
		prometheusNotifier,
	}, nil, wsService)
	require.NoError(t, err)

	services := service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  wsService,
		FlagsetManager:    flagsetManager,
		Metrics:           metricsV2,
	}

	srv := newTestServer(t,
		proxyConf,
		services,
		log.Slog,
		api.WithListener(listener),
		api.WithMonitoringListener(monitoringLn),
	)
	go func() { srv.StartWithContext(context.Background()) }()

	defer srv.Stop(context.Background())

	baseURL := fmt.Sprintf(httpLocalhostD, port)
	monitoringURL := fmt.Sprintf(httpLocalhostD, monitoringPort)

	waitForServer(t, baseURL)
	waitForServer(t, monitoringURL)

	req5, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+health2, http.NoBody)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(req5)
	require.NoError(t, err)

	defer func() { _ = response.Body.Close() }()

	assert.Equal(t, http.StatusNotFound, response.StatusCode)

	req6, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+metrics2, http.NoBody)
	require.NoError(t, err)
	responseM, err := http.DefaultClient.Do(req6)
	require.NoError(t, err)

	defer func() { _ = responseM.Body.Close() }()

	assert.Equal(t, http.StatusNotFound, responseM.StatusCode)

	req7, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+info, http.NoBody)
	require.NoError(t, err)
	responseI, err := http.DefaultClient.Do(req7)
	require.NoError(t, err)

	defer func() { _ = responseI.Body.Close() }()

	assert.Equal(t, http.StatusNotFound, responseI.StatusCode)

	req8, err := http.NewRequestWithContext(t.Context(), http.MethodGet, monitoringURL+health2, http.NoBody)
	require.NoError(t, err)
	responseH1, err := http.DefaultClient.Do(req8)
	require.NoError(t, err)

	defer func() { _ = responseH1.Body.Close() }()

	assert.Equal(t, http.StatusOK, responseH1.StatusCode)

	req9, err := http.NewRequestWithContext(t.Context(), http.MethodGet, monitoringURL+metrics2, http.NoBody)
	require.NoError(t, err)
	responseM1, err := http.DefaultClient.Do(req9)
	require.NoError(t, err)

	defer func() { _ = responseM1.Body.Close() }()

	assert.Equal(t, http.StatusOK, responseM1.StatusCode)

	req10, err := http.NewRequestWithContext(t.Context(), http.MethodGet, monitoringURL+info, http.NoBody)
	require.NoError(t, err)
	responseI1, err := http.DefaultClient.Do(req10)
	require.NoError(t, err)

	defer func() { _ = responseI1.Body.Close() }()

	assert.Equal(t, http.StatusOK, responseI1.StatusCode)
}

func Test_CheckOFREPAPIExists(t *testing.T) {
	t.Parallel()

	listener, port := testutils.ListenLocal(t)
	proxyConf := &config.Config{
		Retrievers: &[]retrieverconf.RetrieverConf{
			{
				Kind: file2,
				Path: testdataFlagConfigYaml,
			},
		},
		Server: config.Server{
			Mode: config.ServerModeHTTP,
			Port: port,
		},
		AuthorizedKeys: config.APIKeys{
			Admin:      nil,
			Evaluation: []string{test2},
		},
	}
	log := newTestLogger(t)

	metricsV2, err := metric.NewMetrics()
	if err != nil {
		log.Slog.LogAttrs(
			context.Background(),
			slog.LevelError,
			impossibleToInitializePrometheus,
			cmdlog.Err(err),
		)
	}

	wsService := stream.NewWebsocketService()
	defer wsService.Close() // close all the open connections

	prometheusNotifier := metric.NewPrometheusNotifier(metricsV2)
	flagsetManager, err := service.NewFlagsetManager(proxyConf, log.Slog, []notifier.Notifier{
		prometheusNotifier,
	}, nil, wsService)
	require.NoError(t, err)

	services := service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  wsService,
		FlagsetManager:    flagsetManager,
		Metrics:           metricsV2,
	}

	s := newTestServer(t, proxyConf, services, log.Slog, api.WithListener(listener))
	go func() { s.StartWithContext(context.Background()) }()

	defer s.Stop(context.Background())

	baseURL := fmt.Sprintf(httpLocalhostD, port)
	waitForServer(t, baseURL)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		baseURL+ofrepV1Evaluate,
		strings.NewReader(contextTargetingKeySomeKey))
	require.NoError(t, err)
	req.Header.Add(authorization, bearerTest)
	req.Header.Add(contentType, applicationJSON)
	response, err := (&http.Client{}).Do(req)
	require.NoError(t, err)

	defer func() { _ = response.Body.Close() }()

	assert.Equal(t, http.StatusOK, response.StatusCode)

	req, err = http.NewRequestWithContext(t.Context(), http.MethodPost,
		baseURL+"/ofrep/v1/evaluate/flags/some-key",
		strings.NewReader(contextTargetingKeySomeKey))
	require.NoError(t, err)
	req.Header.Add(authorization, bearerTest)
	req.Header.Add(contentType, applicationJSON)
	response, err = (&http.Client{}).Do(req)
	require.NoError(t, err)

	defer func() { _ = response.Body.Close() }()

	assert.Equal(t, http.StatusNotFound, response.StatusCode)

	req, err = http.NewRequestWithContext(t.Context(), http.MethodPost,
		baseURL+ofrepV1Evaluate5,
		strings.NewReader(contextTargetingKeySomeKey))
	require.NoError(t, err)
	req.Header.Add(authorization, bearerTest)
	req.Header.Add(contentType, applicationJSON)
	response, err = (&http.Client{}).Do(req)
	require.NoError(t, err)

	defer func() { _ = response.Body.Close() }()

	assert.Equal(t, http.StatusOK, response.StatusCode)
}

func Test_Middleware_VersionHeader_Enabled_Default(t *testing.T) {
	t.Parallel()

	listener, port := testutils.ListenLocal(t)
	proxyConf := &config.Config{
		Retrievers: &[]retrieverconf.RetrieverConf{
			{
				Kind: file2,
				Path: testdataFlagConfigYaml,
			},
		},
		Server: config.Server{
			Mode: config.ServerModeHTTP,
			Port: port,
		},
	}
	log := newTestLogger(t)

	metricsV2, err := metric.NewMetrics()
	require.NoError(t, err)

	wsService := stream.NewWebsocketService()
	defer wsService.Close()

	flagsetManager, err := service.NewFlagsetManager(proxyConf, log.Slog, nil, nil, nil)
	require.NoError(t, err)

	services := service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  wsService,
		FlagsetManager:    flagsetManager,
		Metrics:           metricsV2,
	}

	s := newTestServer(t, proxyConf, services, log.Slog, api.WithListener(listener))
	go func() { s.StartWithContext(context.Background()) }()

	defer s.Stop(context.Background())

	baseURL := fmt.Sprintf(httpLocalhostD, port)
	waitForServer(t, baseURL)

	req11, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+health2, http.NoBody)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(req11)
	require.NoError(t, err)

	defer func() { _ = response.Body.Close() }()

	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, proxyConf.Version, response.Header.Get(xGofeatureflagVersion))
}

func Test_VersionHeader_Disabled(t *testing.T) {
	t.Parallel()

	listener, port := testutils.ListenLocal(t)
	proxyConf := &config.Config{
		Retrievers: &[]retrieverconf.RetrieverConf{
			{
				Kind: file2,
				Path: testdataFlagConfigYaml,
			},
		},
		Server: config.Server{
			Mode: config.ServerModeHTTP,
			Port: port,
		},
		DisableVersionHeader: true,
	}
	log := newTestLogger(t)

	metricsV2, err := metric.NewMetrics()
	require.NoError(t, err)

	wsService := stream.NewWebsocketService()
	defer wsService.Close()

	flagsetManager, err := service.NewFlagsetManager(proxyConf, log.Slog, nil, nil, nil)
	require.NoError(t, err)

	services := service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  wsService,
		FlagsetManager:    flagsetManager,
		Metrics:           metricsV2,
	}

	s := newTestServer(t, proxyConf, services, log.Slog, api.WithListener(listener))
	go func() { s.StartWithContext(context.Background()) }()

	defer s.Stop(context.Background())

	baseURL := fmt.Sprintf(httpLocalhostD, port)
	waitForServer(t, baseURL)

	req12, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+health2, http.NoBody)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(req12)
	require.NoError(t, err)

	defer func() { _ = response.Body.Close() }()

	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Empty(t, response.Header.Get(xGofeatureflagVersion))
}

func Test_AuthenticationMiddleware(t *testing.T) {
	t.Parallel()

	t.Run("Non Admin Endpoint", func(t *testing.T) {
		t.Parallel()

		tests := []authMiddlewareTestCase{
			{
				name:          "Authentication disabled",
				configAPIKeys: nil,
				want:          http.StatusOK,
			},
			{
				name:          "Evaluation key provided",
				configAPIKeys: &config.APIKeys{Evaluation: []string{test2}},
				want:          http.StatusUnauthorized,
			},
			{
				name:          "Admin key provided, no evaluation key provided",
				configAPIKeys: &config.APIKeys{Admin: []string{test2}},
				want:          http.StatusOK,
			},
			{
				name:          "Evaluation and Admin key provided",
				configAPIKeys: &config.APIKeys{Evaluation: []string{test2}, Admin: []string{test2}},
				want:          http.StatusUnauthorized,
			},
		}

		runAuthMiddlewareTests(
			t,
			tests,
			func(_ *testing.T, _ authMiddlewareTestCase, baseURL string) (*http.Response, error) {
				req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, baseURL+ofrepV1Evaluate5,
					strings.NewReader(contextTargetingKeySomeKey2))
				if err != nil {
					return nil, err
				}
				req.Header.Set("Content-Type", applicationJSON)

				return http.DefaultClient.Do(req)
			})
	})

	t.Run("Admin Endpoint", func(t *testing.T) {
		t.Parallel()

		tests := []authMiddlewareTestCase{
			{
				name:          "Authentication disabled",
				configAPIKeys: nil,
				want:          http.StatusUnauthorized,
			},
			{
				name:          "Evaluation key provided",
				configAPIKeys: &config.APIKeys{Evaluation: []string{test2}},
				want:          http.StatusUnauthorized,
			},
			{
				name:          "Admin key provided, no evaluation key provided",
				configAPIKeys: &config.APIKeys{Admin: []string{test2}},
				want:          http.StatusOK,
			},
			{
				name:          "Evaluation and Admin key provided",
				configAPIKeys: &config.APIKeys{Evaluation: []string{test2}, Admin: []string{test2}},
				want:          http.StatusOK,
			},
		}

		runAuthMiddlewareTests(
			t,
			tests,
			func(t *testing.T, tt authMiddlewareTestCase, baseURL string) (*http.Response, error) {
				t.Helper()

				request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+adminV1Retriever, http.NoBody)
				require.NoError(t, err)
				request.Header.Add(contentType, applicationJSON)

				if tt.configAPIKeys != nil && len(tt.configAPIKeys.Admin) > 0 {
					request.Header.Add(authorization, bearer2+tt.configAPIKeys.Admin[0])
				}

				return (&http.Client{}).Do(request)
			})
	})
}

type authMiddlewareTestCase struct {
	configAPIKeys *config.APIKeys
	name          string
	want          int // http status code
}

// runAuthMiddlewareTests starts a relay proxy for each test case, issues the
// request returned by doRequest, and asserts the resulting HTTP status code.
// It holds the shared table-test setup so Test_AuthenticationMiddleware stays flat.
func runAuthMiddlewareTests(
	t *testing.T,
	tests []authMiddlewareTestCase,
	doRequest func(t *testing.T, tt authMiddlewareTestCase, baseURL string) (*http.Response, error),
) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listener, port := testutils.ListenLocal(t)

			proxyConf := &config.Config{
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: file2,
						Path: testdataFlagConfigYaml,
					},
				},
				Server: config.Server{
					Mode: config.ServerModeHTTP,
					Port: port,
				},
				DisableVersionHeader: true,
			}
			if tt.configAPIKeys != nil {
				proxyConf.AuthorizedKeys = *tt.configAPIKeys
			}

			proxyConf.ForceReloadAPIKeys()

			log := newTestLogger(t)

			metricsV2, err := metric.NewMetrics()
			require.NoError(t, err)

			wsService := stream.NewWebsocketService()
			defer wsService.Close()

			flagsetManager, err := service.NewFlagsetManager(proxyConf, log.Slog, nil, nil, nil)
			require.NoError(t, err)

			services := service.Services{
				MonitoringService: service.NewMonitoring(flagsetManager),
				WebsocketService:  wsService,
				FlagsetManager:    flagsetManager,
				Metrics:           metricsV2,
			}

			s := newTestServer(t, proxyConf, services, log.Slog, api.WithListener(listener))
			go func() { s.StartWithContext(context.Background()) }()

			defer s.Stop(context.Background())

			baseURL := fmt.Sprintf(httpLocalhostD, port)
			waitForServer(t, baseURL)

			response, err := doRequest(t, tt, baseURL)
			require.NoError(t, err)

			defer func() { _ = response.Body.Close() }()

			assert.Equal(t, tt.want, response.StatusCode)
		})
	}
}

// skipUnixSocketOnWindows skips the unix socket tests on Windows. Binding an AF_UNIX listener
// there intermittently fails on the CI runners with WSAENETDOWN ("a socket operation encountered
// a dead network"), and unix socket mode is a Unix-only deployment target anyway.
func skipUnixSocketOnWindows(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("AF_UNIX listeners are unreliable on Windows CI (WSAENETDOWN); " +
			"unix socket mode is a Unix-only deployment target")
	}
}

// Helper function to create an HTTP client that can connect via Unix socket.
func newUnixSocketHTTPClient(socketPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, unix, socketPath)
			},
		},
	}
}

func Test_Starting_RelayProxy_UnixSocket(t *testing.T) {
	t.Parallel()

	skipUnixSocketOnWindows(t)
	// Create a temporary directory for the socket
	tempDir := shortSocketDir(t)

	socketPath := filepath.Join(tempDir, "goff-test.sock")

	proxyConf := &config.Config{
		Retrievers: &[]retrieverconf.RetrieverConf{
			{
				Kind: file2,
				Path: testdataFlagConfigYaml,
			},
		},
		Server: config.Server{
			Mode:           config.ServerModeUnixSocket,
			UnixSocketPath: socketPath,
		},
	}
	log := newTestLogger(t)

	metricsV2, err := metric.NewMetrics()
	if err != nil {
		log.Slog.LogAttrs(
			context.Background(),
			slog.LevelError,
			impossibleToInitializePrometheus,
			cmdlog.Err(err),
		)
	}

	wsService := stream.NewWebsocketService()
	defer wsService.Close()

	prometheusNotifier := metric.NewPrometheusNotifier(metricsV2)
	flagsetManager, err := service.NewFlagsetManager(proxyConf, log.Slog, []notifier.Notifier{
		prometheusNotifier,
	}, nil, wsService)
	require.NoError(t, err)

	services := service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  wsService,
		FlagsetManager:    flagsetManager,
		Metrics:           metricsV2,
	}

	s := newTestServer(t, proxyConf, services, log.Slog)
	go func() { s.StartWithContext(context.Background()) }()

	defer s.Stop(context.Background())

	// Wait for the socket to be created
	require.Eventually(t, func() bool {
		// The socket file exists as soon as the server binds, before it listens: dial it.
		conn, dialErr := (&net.Dialer{}).DialContext(t.Context(), unix, socketPath)
		if dialErr != nil {
			return false
		}

		_ = conn.Close()

		return true
	}, 1*time.Second, 10*time.Millisecond, unixSocketFileWas)

	// Verify socket file exists
	_, err = os.Stat(socketPath)
	require.NoError(t, err, unixSocketFileShould)

	// Create a Unix socket HTTP client
	client := newUnixSocketHTTPClient(socketPath)

	// Test health endpoint
	req13, err := http.NewRequestWithContext(t.Context(), http.MethodGet, httpUnixHealth, http.NoBody)
	require.NoError(t, err)
	response, err := client.Do(req13)
	require.NoError(t, err)

	defer func() { _ = response.Body.Close() }()

	assert.Equal(t, http.StatusOK, response.StatusCode)

	// Test metrics endpoint
	req14, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://unix/metrics", http.NoBody)
	require.NoError(t, err)
	responseM, err := client.Do(req14)
	require.NoError(t, err)

	defer func() { _ = responseM.Body.Close() }()

	assert.Equal(t, http.StatusOK, responseM.StatusCode)

	// Test info endpoint
	req15, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://unix/info", http.NoBody)
	require.NoError(t, err)
	responseI, err := client.Do(req15)
	require.NoError(t, err)

	defer func() { _ = responseI.Body.Close() }()

	assert.Equal(t, http.StatusOK, responseI.StatusCode)
}

func Test_Starting_RelayProxy_UnixSocket_MonitoringPort(t *testing.T) {
	t.Parallel()

	skipUnixSocketOnWindows(t)
	monitoringLn, monitoringPort := testutils.ListenLocal(t)
	// Create a temporary directory for the socket
	tempDir := shortSocketDir(t)

	socketPath := filepath.Join(tempDir, "goff-test.sock")

	proxyConf := &config.Config{
		Retrievers: &[]retrieverconf.RetrieverConf{
			{
				Kind: file2,
				Path: testdataFlagConfigYaml,
			},
		},
		Server: config.Server{
			Mode:           config.ServerModeUnixSocket,
			UnixSocketPath: socketPath,
			MonitoringPort: monitoringPort,
		},
	}
	log := newTestLogger(t)

	metricsV2, err := metric.NewMetrics()
	if err != nil {
		log.Slog.LogAttrs(
			context.Background(),
			slog.LevelError,
			impossibleToInitializePrometheus,
			cmdlog.Err(err),
		)
	}

	wsService := stream.NewWebsocketService()
	defer wsService.Close()

	prometheusNotifier := metric.NewPrometheusNotifier(metricsV2)
	flagsetManager, err := service.NewFlagsetManager(proxyConf, log.Slog, []notifier.Notifier{
		prometheusNotifier,
	}, nil, wsService)
	require.NoError(t, err)

	services := service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  wsService,
		FlagsetManager:    flagsetManager,
		Metrics:           metricsV2,
	}

	s := newTestServer(t, proxyConf, services, log.Slog, api.WithMonitoringListener(monitoringLn))
	go func() { s.StartWithContext(context.Background()) }()

	defer s.Stop(context.Background())

	// Wait for the socket to be created
	require.Eventually(t, func() bool {
		// The socket file exists as soon as the server binds, before it listens: dial it.
		conn, dialErr := (&net.Dialer{}).DialContext(t.Context(), unix, socketPath)
		if dialErr != nil {
			return false
		}

		_ = conn.Close()

		return true
	}, 1*time.Second, 10*time.Millisecond, unixSocketFileWas)

	// Verify socket file exists
	_, err = os.Stat(socketPath)
	require.NoError(t, err, unixSocketFileShould)

	monitoringURL := fmt.Sprintf(httpLocalhostD, monitoringPort)
	waitForServer(t, monitoringURL)

	// Test health endpoint
	req16, err := http.NewRequestWithContext(t.Context(), http.MethodGet, monitoringURL+health2, http.NoBody)
	require.NoError(t, err)
	responseH1, err := http.DefaultClient.Do(req16)
	require.NoError(t, err)

	defer responseH1.Body.Close()

	assert.Equal(t, http.StatusOK, responseH1.StatusCode)

	// Test metrics endpoint
	req17, err := http.NewRequestWithContext(t.Context(), http.MethodGet, monitoringURL+metrics2, http.NoBody)
	require.NoError(t, err)
	responseM1, err := http.DefaultClient.Do(req17)
	require.NoError(t, err)

	defer responseM1.Body.Close()

	assert.Equal(t, http.StatusOK, responseM1.StatusCode)

	// Test info endpoint
	req18, err := http.NewRequestWithContext(t.Context(), http.MethodGet, monitoringURL+info, http.NoBody)
	require.NoError(t, err)
	responseI, err := http.DefaultClient.Do(req18)
	require.NoError(t, err)

	defer responseI.Body.Close()

	assert.Equal(t, http.StatusOK, responseI.StatusCode)
}

func Test_Starting_RelayProxy_UnixSocket_OFREP_API(t *testing.T) {
	t.Parallel()

	skipUnixSocketOnWindows(t)
	// Create a temporary directory for the socket
	tempDir := shortSocketDir(t)

	socketPath := filepath.Join(tempDir, "goff-test-ofrep.sock")

	proxyConf := &config.Config{
		Retrievers: &[]retrieverconf.RetrieverConf{
			{
				Kind: file2,
				Path: testdataFlagConfigYaml,
			},
		},
		Server: config.Server{
			Mode:           config.ServerModeUnixSocket,
			UnixSocketPath: socketPath,
		},
		AuthorizedKeys: config.APIKeys{
			Admin:      nil,
			Evaluation: []string{test2},
		},
	}
	log := newTestLogger(t)

	metricsV2, err := metric.NewMetrics()
	if err != nil {
		log.Slog.LogAttrs(
			context.Background(),
			slog.LevelError,
			impossibleToInitializePrometheus,
			cmdlog.Err(err),
		)
	}

	wsService := stream.NewWebsocketService()
	defer wsService.Close()

	prometheusNotifier := metric.NewPrometheusNotifier(metricsV2)
	flagsetManager, err := service.NewFlagsetManager(proxyConf, log.Slog, []notifier.Notifier{
		prometheusNotifier,
	}, nil, wsService)
	require.NoError(t, err)

	services := service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  wsService,
		FlagsetManager:    flagsetManager,
		Metrics:           metricsV2,
	}

	s := newTestServer(t, proxyConf, services, log.Slog)
	go func() { s.StartWithContext(context.Background()) }()

	defer s.Stop(context.Background())

	// Wait for the socket to be created
	require.Eventually(t, func() bool {
		// The socket file exists as soon as the server binds, before it listens: dial it.
		conn, dialErr := (&net.Dialer{}).DialContext(t.Context(), unix, socketPath)
		if dialErr != nil {
			return false
		}

		_ = conn.Close()

		return true
	}, 1*time.Second, 10*time.Millisecond, unixSocketFileWas)

	// Verify socket file exists
	_, err = os.Stat(socketPath)
	require.NoError(t, err, unixSocketFileShould)

	// Create a Unix socket HTTP client
	client := newUnixSocketHTTPClient(socketPath)

	// Test OFREP evaluate all flags endpoint
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		"http://unix/ofrep/v1/evaluate/flags",
		strings.NewReader(contextTargetingKeySomeKey))
	require.NoError(t, err)
	req.Header.Add(authorization, bearerTest)
	req.Header.Add(contentType, applicationJSON)
	response, err := client.Do(req)
	require.NoError(t, err)

	defer func() { _ = response.Body.Close() }()

	assert.Equal(t, http.StatusOK, response.StatusCode)

	// Test OFREP evaluate specific flag endpoint (non-existent flag)
	req, err = http.NewRequestWithContext(t.Context(), http.MethodPost,
		"http://unix/ofrep/v1/evaluate/flags/some-key",
		strings.NewReader(contextTargetingKeySomeKey))
	require.NoError(t, err)
	req.Header.Add(authorization, bearerTest)
	req.Header.Add(contentType, applicationJSON)
	response, err = client.Do(req)
	require.NoError(t, err)

	defer func() { _ = response.Body.Close() }()

	assert.Equal(t, http.StatusNotFound, response.StatusCode)

	// Test OFREP evaluate specific flag endpoint (existing flag)
	req, err = http.NewRequestWithContext(t.Context(), http.MethodPost,
		httpUnixOfrepV,
		strings.NewReader(contextTargetingKeySomeKey))
	require.NoError(t, err)
	req.Header.Add(authorization, bearerTest)
	req.Header.Add(contentType, applicationJSON)
	response, err = client.Do(req)
	require.NoError(t, err)

	defer func() { _ = response.Body.Close() }()

	assert.Equal(t, http.StatusOK, response.StatusCode)
}

func TestStartingRelayProxyUnixSocketAuthentication(t *testing.T) {
	t.Parallel()

	skipUnixSocketOnWindows(t)

	tests := []struct {
		name          string
		configAPIKeys *config.APIKeys
		endpoint      string
		method        string
		body          string
		authHeader    string
		want          int // http status code
	}{
		{
			name:          "Authentication disabled - health endpoint",
			configAPIKeys: nil,
			endpoint:      httpUnixHealth,
			method:        methodGET,
			want:          http.StatusOK,
		},
		{
			name:          "Evaluation endpoint - with valid key",
			configAPIKeys: &config.APIKeys{Evaluation: []string{"test-key"}},
			endpoint:      httpUnixOfrepV,
			method:        pOST,
			body:          contextTargetingKeySomeKey2,
			authHeader:    "Bearer test-key",
			want:          http.StatusOK,
		},
		{
			name:          "Evaluation endpoint - without key (should fail)",
			configAPIKeys: &config.APIKeys{Evaluation: []string{"test-key"}},
			endpoint:      httpUnixOfrepV,
			method:        pOST,
			body:          contextTargetingKeySomeKey2,
			authHeader:    "",
			want:          http.StatusUnauthorized,
		},
		{
			name:          "Admin endpoint - with valid admin key",
			configAPIKeys: &config.APIKeys{Admin: []string{adminKey}},
			endpoint:      "http://unix/admin/v1/retriever/refresh",
			method:        pOST,
			authHeader:    "Bearer admin-key",
			want:          http.StatusOK,
		},
		{
			name:          "Admin endpoint - without admin key (should fail)",
			configAPIKeys: &config.APIKeys{Admin: []string{adminKey}},
			endpoint:      "http://unix/admin/v1/retriever/refresh",
			method:        pOST,
			authHeader:    "",
			want:          http.StatusUnauthorized,
		},
		{
			name:          "Admin validate endpoint - with valid admin key",
			configAPIKeys: &config.APIKeys{Admin: []string{adminKey}},
			endpoint:      "http://unix/admin/v1/flags/validate",
			method:        pOST,
			body:          `{"my-flag":{"variations":{"a":true,"b":false},"defaultRule":{"variation":"a"}}}`,
			authHeader:    "Bearer admin-key",
			want:          http.StatusOK,
		},
		{
			name:          "Admin validate endpoint - without admin key (should fail)",
			configAPIKeys: &config.APIKeys{Admin: []string{adminKey}},
			endpoint:      "http://unix/admin/v1/flags/validate",
			method:        pOST,
			body:          `{"my-flag":{"variations":{"a":true,"b":false},"defaultRule":{"variation":"a"}}}`,
			authHeader:    "",
			want:          http.StatusUnauthorized,
		},
	}

	// Short path: t.TempDir() embeds the test name and would push a unix socket path over the OS limit.
	tempDir := shortSocketDir(t)

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create a temporary directory for the socket

			socketPath := filepath.Join(tempDir, fmt.Sprintf("goff-test-%d.sock", index))

			proxyConf := &config.Config{
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: file2,
						Path: testdataFlagConfigYaml,
					},
				},
				Server: config.Server{
					Mode:           config.ServerModeUnixSocket,
					UnixSocketPath: socketPath,
				},
			}
			if tt.configAPIKeys != nil {
				proxyConf.AuthorizedKeys = *tt.configAPIKeys
			}

			proxyConf.ForceReloadAPIKeys()

			log := newTestLogger(t)

			metricsV2, err := metric.NewMetrics()
			require.NoError(t, err)

			wsService := stream.NewWebsocketService()
			defer wsService.Close()

			flagsetManager, err := service.NewFlagsetManager(proxyConf, log.Slog, nil, nil, nil)
			require.NoError(t, err)

			services := service.Services{
				MonitoringService: service.NewMonitoring(flagsetManager),
				WebsocketService:  wsService,
				FlagsetManager:    flagsetManager,
				Metrics:           metricsV2,
			}

			s := newTestServer(t, proxyConf, services, log.Slog)
			go func() { s.StartWithContext(context.Background()) }()

			defer s.Stop(context.Background())

			// Wait for the socket to be created
			require.Eventually(t, func() bool {
				// The socket file exists as soon as the server binds, before it listens: dial it.
				conn, dialErr := (&net.Dialer{}).DialContext(t.Context(), unix, socketPath)
				if dialErr != nil {
					return false
				}

				_ = conn.Close()

				return true
			}, 1*time.Second, 10*time.Millisecond, unixSocketFileWas)

			// Create a Unix socket HTTP client
			client := newUnixSocketHTTPClient(socketPath)

			// Create and execute request
			var req *http.Request
			if tt.body != "" {
				req, err = http.NewRequestWithContext(t.Context(), tt.method, tt.endpoint, strings.NewReader(tt.body))
			} else {
				req, err = http.NewRequestWithContext(t.Context(), tt.method, tt.endpoint, http.NoBody)
			}

			require.NoError(t, err)

			if tt.authHeader != "" {
				req.Header.Add(authorization, tt.authHeader)
			}

			if tt.body != "" {
				req.Header.Add(contentType, applicationJSON)
			}

			response, err := client.Do(req)
			require.NoError(t, err)

			defer func() { _ = response.Body.Close() }()

			assert.Equal(t, tt.want, response.StatusCode)
		})
	}
}

func TestStartingRelayProxyUnixSocketVersionHeader(t *testing.T) {
	t.Parallel()

	skipUnixSocketOnWindows(t)

	tests := []struct {
		name                 string
		disableVersionHeader bool
		wantVersionHeader    bool
	}{
		{
			name:                 "Version header enabled by default",
			disableVersionHeader: false,
			wantVersionHeader:    true,
		},
		{
			name:                 "Version header disabled",
			disableVersionHeader: true,
			wantVersionHeader:    false,
		},
	}

	// Short path: t.TempDir() embeds the test name and would push a unix socket path over the OS limit.
	tempDir := shortSocketDir(t)

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create a temporary directory for the socket

			socketPath := filepath.Join(tempDir, fmt.Sprintf("goff-test-version-%d.sock", index))

			proxyConf := &config.Config{
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: file2,
						Path: testdataFlagConfigYaml,
					},
				},
				Server: config.Server{
					Mode:           config.ServerModeUnixSocket,
					UnixSocketPath: socketPath,
				},
				DisableVersionHeader: tt.disableVersionHeader,
				Version:              "test-version-1.0.0",
			}

			log := newTestLogger(t)

			metricsV2, err := metric.NewMetrics()
			require.NoError(t, err)

			wsService := stream.NewWebsocketService()
			defer wsService.Close()

			flagsetManager, err := service.NewFlagsetManager(proxyConf, log.Slog, nil, nil, nil)
			require.NoError(t, err)

			services := service.Services{
				MonitoringService: service.NewMonitoring(flagsetManager),
				WebsocketService:  wsService,
				FlagsetManager:    flagsetManager,
				Metrics:           metricsV2,
			}

			s := newTestServer(t, proxyConf, services, log.Slog)
			go func() { s.StartWithContext(context.Background()) }()

			defer s.Stop(context.Background())

			// Wait for the socket to be created
			require.Eventually(t, func() bool {
				// The socket file exists as soon as the server binds, before it listens: dial it.
				conn, dialErr := (&net.Dialer{}).DialContext(t.Context(), unix, socketPath)
				if dialErr != nil {
					return false
				}

				_ = conn.Close()

				return true
			}, 1*time.Second, 10*time.Millisecond, unixSocketFileWas)

			// Create a Unix socket HTTP client
			client := newUnixSocketHTTPClient(socketPath)

			req19, err := http.NewRequestWithContext(t.Context(), http.MethodGet, httpUnixHealth, http.NoBody)
			require.NoError(t, err)
			response, err := client.Do(req19)
			require.NoError(t, err)

			defer func() { _ = response.Body.Close() }()

			assert.Equal(t, http.StatusOK, response.StatusCode)

			if tt.wantVersionHeader {
				assert.Equal(t, "test-version-1.0.0", response.Header.Get(xGofeatureflagVersion))
			} else {
				assert.Empty(t, response.Header.Get(xGofeatureflagVersion))
			}
		})
	}
}

func Test_NativeHistograms_Enabled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	listener, port := testutils.ListenLocal(t)
	proxyConf := &config.Config{
		Retrievers: &[]retrieverconf.RetrieverConf{
			{
				Kind: file2,
				Path: testdataFlagConfigYaml,
			},
		},
		Server: config.Server{
			Mode: config.ServerModeHTTP,
			Port: port,
		},
	}

	log := newTestLogger(t)

	metricsV2, err := metric.NewMetrics()
	require.NoError(t, err)

	wsService := stream.NewWebsocketService()
	defer wsService.Close()

	prometheusNotifier := metric.NewPrometheusNotifier(metricsV2)

	flagsetManager, err := service.NewFlagsetManager(proxyConf, log.Slog, []notifier.Notifier{
		prometheusNotifier,
	}, nil, wsService)
	require.NoError(t, err)

	services := service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  wsService,
		FlagsetManager:    flagsetManager,
		Metrics:           metricsV2,
	}

	s := newTestServer(t, proxyConf, services, log.Slog, api.WithListener(listener))
	go func() { s.StartWithContext(ctx) }()

	defer s.Stop(ctx)

	baseURL := fmt.Sprintf(httpLocalhostD, port)
	waitForServer(t, baseURL)

	// Make some requests to generate histogram data
	req20, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+health2, http.NoBody)
	require.NoError(t, err)
	healthResp, err := http.DefaultClient.Do(req20)
	require.NoError(t, err)

	_ = healthResp.Body.Close()

	req21, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+info, http.NoBody)
	require.NoError(t, err)
	infoResp, err := http.DefaultClient.Do(req21)
	require.NoError(t, err)

	_ = infoResp.Body.Close()

	// Scrape /metrics with protobuf Accept header
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+metrics2, http.NoBody)
	require.NoError(t, err)
	req.Header.Set(
		"Accept",
		"application/vnd.google.protobuf;proto=io.prometheus.client.MetricFamily;encoding=delimited",
	)

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Decode delimited protobuf stream
	var histogramFamilies []*promdto.MetricFamily

	reader := bufio.NewReader(resp.Body)

	for {
		var family promdto.MetricFamily

		unmarshalFromErr := protodelim.UnmarshalFrom(reader, &family)
		if errors.Is(unmarshalFromErr, io.EOF) {
			break
		}

		require.NoError(t, unmarshalFromErr)

		// Only check histogram metric families from our subsystem
		if family.GetType() == promdto.MetricType_HISTOGRAM {
			if strings.HasPrefix(family.GetName(), "semaphore_") {
				histogramFamilies = append(histogramFamilies, &family)
			}
		}
	}

	// Assert we found at least one histogram metric family
	require.NotEmpty(t, histogramFamilies)

	// Check each histogram has both classic buckets and native histogram fields
	for _, family := range histogramFamilies {
		for _, metric := range family.GetMetric() {
			hist := metric.GetHistogram()
			if hist == nil {
				continue
			}

			// Assert classic histogram buckets are still present
			assert.NotEmpty(t, hist.GetBucket())

			// Assert native histogram data is present
			// Check for native histogram fields: PositiveSpan, NegativeSpan, or Schema
			hasNativeData := len(hist.GetPositiveSpan()) > 0 ||
				len(hist.GetNegativeSpan()) > 0 ||
				hist.Schema != nil
			assert.True(t, hasNativeData)
		}
	}
}

func Test_PortFreedAfterShutdown(t *testing.T) {
	t.Parallel()

	// The server serves on a listener that stays bound from the start: the server closing it is what frees the port.
	listener, port := testutils.ListenLocal(t)

	proxyConf := &config.Config{
		Retrievers: &[]retrieverconf.RetrieverConf{
			{
				Kind: file2,
				Path: testdataFlagConfigYaml,
			},
		},
		Server: config.Server{
			Mode: config.ServerModeHTTP,
			Port: port,
		},
	}

	testLogger := newTestLogger(t)

	wsService := stream.NewWebsocketService()

	flagsetManager, err := service.NewFlagsetManager(proxyConf, testLogger.Slog, nil, nil, nil)
	require.NoError(t, err)

	services := service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  wsService,
		FlagsetManager:    flagsetManager,
	}

	srv := newTestServer(t, proxyConf, services, testLogger.Slog, api.WithListener(listener))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		srv.StartWithContext(ctx)
		close(done)
	}()

	// Wait for the server to be ready.
	require.Eventually(t, func() bool {
		req22, getErr := http.NewRequestWithContext(t.Context(), http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/health", port), http.NoBody)
		require.NoError(t, getErr)
		resp, getErr := http.DefaultClient.Do(req22)
		if getErr != nil {
			return false
		}

		_ = resp.Body.Close()

		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 50*time.Millisecond)

	// Cancel the context to simulate Ctrl+C.
	cancel()
	wsService.Close()

	// Wait for StartWithContext to return.
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		require.Fail(t, "server did not stop within 10 seconds")
	}

	// Verify the port is free by binding to it again.
	ln2, err := (&net.ListenConfig{}).Listen(t.Context(), tcp2, fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err, "port %d should be free after shutdown", port)
	ln2.Close()
}

func Test_StopBeforeStart(t *testing.T) {
	t.Parallel()

	// The server is given a listener that stays bound: closing it and binding the port again later would let
	// another test take the port in between, and the server closes it when it is stopped before it started.
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), tcp2, "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	require.True(t, ok)

	port := tcpAddr.Port

	proxyConf := &config.Config{
		Retrievers: &[]retrieverconf.RetrieverConf{
			{
				Kind: file2,
				Path: testdataFlagConfigYaml,
			},
		},
		Server: config.Server{
			Mode: config.ServerModeHTTP,
			Port: port,
		},
	}

	testLogger := newTestLogger(t)
	flagsetManager, err := service.NewFlagsetManager(proxyConf, testLogger.Slog, nil, nil, nil)
	require.NoError(t, err)

	services := service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  stream.NewWebsocketService(),
		FlagsetManager:    flagsetManager,
	}
	srv := newTestServer(t, proxyConf, services, testLogger.Slog, api.WithListener(listener))

	// StartWithContext has not run, so Stop cannot see the servers drain and must give up
	// when its context is done instead of blocking forever.
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelStop()

	stopReturned := make(chan struct{})

	go func() {
		srv.Stop(stopCtx)
		close(stopReturned)
	}()

	select {
	case <-stopReturned:
	case <-time.After(5 * time.Second):
		require.Fail(t, "Stop did not return after its context was done")
	}

	// A server stopped before it started must return immediately without serving.
	done := make(chan struct{})

	go func() {
		srv.StartWithContext(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		require.Fail(t, "StartWithContext did not return for a server already stopped")
	}

	// The server never served and it closed the listener it owns: closing it again fails.
	require.ErrorIs(t, listener.Close(), net.ErrClosed, "the listener of port %d must be closed by the server", port)
}

// shortSocketDir returns a directory whose path is short enough for a unix socket (about 104 bytes at most).
func shortSocketDir(tb testing.TB) string {
	tb.Helper()

	// t.TempDir() embeds the test name: too long for a socket path.
	//nolint:usetesting // t.TempDir() embeds the test name, exceeding the ~104-byte unix socket path limit
	dir, err := os.MkdirTemp("", "sock")
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = os.RemoveAll(dir) })

	return dir
}
