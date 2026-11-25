// Package gopherstack starts the gopherstack AWS emulator (https://github.com/blackbirdworks/gopherstack)
// in a Docker container, to run end-to-end tests of the AWS-backed components (S3, Kinesis, SQS).
//
// One emulator is shared by all the tests of a package: the TestMain of the package runs Main, which
// starts it and removes it when the tests are done, and the tests get it with Connect.
//
// Environment variables:
//   - GOPHERSTACK_ENDPOINT reuses an already running instance (ex: http://127.0.0.1:8000)
//     instead of starting a container.
//   - GOPHERSTACK_IMAGE overrides Image.
package gopherstack

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// Timeouts of the gopherstack container and the longest prefix of the generated names.
	startupTimeout = 60 * time.Second
	probeTimeout   = 5 * time.Second
	probeInterval  = 200 * time.Millisecond
	maxNameBase    = 30
)

const (
	// Image is the pinned gopherstack image (sha256:3683df48d7ed91df17214e3b6169609420d0d3a59b21a26f808b556be4cd8f78).
	Image = "ghcr.io/blackbirdworks/gopherstack:1.9.1"
	// Port is the port gopherstack listens on in the container.
	Port = "8000/tcp"
	// HealthPath is the health check endpoint.
	HealthPath = "/_gopherstack/health"

	// Region is the region used by the tests.
	Region = "us-east-1"
	// AccessKey is the (not validated) access key id used by the tests.
	AccessKey = "test"
	// SecretKey is the (not validated) secret access key used by the tests.
	SecretKey = "test"

	// EnvEndpoint is the env var to reuse a running instance.
	EnvEndpoint = "GOPHERSTACK_ENDPOINT"
	// EnvImage is the env var to override Image.
	EnvImage = "GOPHERSTACK_IMAGE"
)

// Server is a running gopherstack instance.
type Server struct {
	container testcontainers.Container
	// Endpoint is the base URL (ex: http://127.0.0.1:49153). An IP host makes the AWS SDK use
	// path-style S3 addressing, so no DNS is needed.
	Endpoint string
}

// Start starts a container (or attaches to GOPHERSTACK_ENDPOINT) and waits until it is healthy.
func Start(ctx context.Context) (*Server, error) {
	if endpoint := strings.TrimRight(os.Getenv(EnvEndpoint), "/"); endpoint != "" {
		err := waitHealthy(ctx, endpoint)
		if err != nil {
			return nil, err
		}

		return &Server{Endpoint: endpoint}, nil
	}
	image := Image
	if v := os.Getenv(EnvImage); v != "" {
		image = v
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		Image:        image,
		ExposedPorts: []string{Port},
		WaitingFor: wait.ForHTTP(HealthPath).
			WithPort(Port).
			WithStartupTimeout(startupTimeout),
		Started: true,
	})
	if err != nil {
		if container != nil {
			_ = testcontainers.TerminateContainer(container)
		}

		return nil, fmt.Errorf("start gopherstack: %w", err)
	}
	host, err := container.Host(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(container)

		return nil, err
	}
	if host == "localhost" {
		host = "127.0.0.1" // keep an IP host for path-style S3
	}
	port, err := container.MappedPort(ctx, Port)
	if err != nil {
		_ = testcontainers.TerminateContainer(container)

		return nil, err
	}
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(host, port.Port())}

	return &Server{Endpoint: u.String(), container: container}, nil
}

func waitHealthy(ctx context.Context, endpoint string) error {
	ctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	client := &http.Client{Timeout: probeTimeout}
	for {
		//nolint:gosec // own container or GOPHERSTACK_ENDPOINT
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+HealthPath, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req) //nolint:gosec // same endpoint
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("gopherstack at %s is not healthy: %w", endpoint, ctx.Err())
		case <-time.After(probeInterval):
		}
	}
}

// Terminate removes the container (no-op for an external instance).
func (s *Server) Terminate() error {
	if s == nil || s.container == nil {
		return nil
	}

	return testcontainers.TerminateContainer(s.container)
}

// AwsConfig returns an AWS config with static credentials, the region and the emulator endpoint.
func (s *Server) AwsConfig(ctx context.Context) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx,
		config.WithRegion(Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(AccessKey, SecretKey, "")),
		config.WithBaseEndpoint(s.Endpoint),
	)
}

// MustAwsConfig is AwsConfig failing the test on error.
func (s *Server) MustAwsConfig(tb testing.TB) aws.Config {
	tb.Helper()
	cfg, err := s.AwsConfig(tb.Context())
	require.NoError(tb, err)

	return cfg
}

// SetAwsEnv points the AWS SDK default config chain (used by the relay proxy, which has no
// endpoint setting) to the emulator. It uses t.Setenv so the calling test can't be parallel.
func (s *Server) SetAwsEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_ENDPOINT_URL", s.Endpoint)
	t.Setenv("AWS_ENDPOINT_URL_S3", s.Endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", AccessKey)
	t.Setenv("AWS_SECRET_ACCESS_KEY", SecretKey)
	t.Setenv("AWS_REGION", Region)
	t.Setenv("AWS_DEFAULT_REGION", Region)
	// do not let a developer profile or IMDS interfere
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", os.DevNull)
	t.Setenv("AWS_CONFIG_FILE", os.DevNull)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

// Main is the body of the TestMain of a package that runs end-to-end tests against the emulator. It
// starts one emulator for the package (unless GOPHERSTACK_ENDPOINT already points to one) when Docker
// is available, publishes its endpoint in GOPHERSTACK_ENDPOINT for Connect, runs the tests and removes
// the container. When Docker is not available the tests that call Connect are skipped.
//
//	func TestMain(m *testing.M) { os.Exit(gopherstack.Main(m)) }
func Main(m *testing.M) int {
	ctx := context.Background()
	if os.Getenv(EnvEndpoint) == "" {
		provider, err := testcontainers.NewDockerProvider()
		if err != nil || provider.Health(ctx) != nil {
			return m.Run() // Connect skips the tests
		}
		_ = provider.Close()
		srv, err := Start(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gopherstack:", err)

			return 1
		}
		defer func() { _ = srv.Terminate() }()
		setenvErr := os.Setenv(EnvEndpoint, srv.Endpoint)
		if setenvErr != nil {
			fmt.Fprintln(os.Stderr, "gopherstack:", setenvErr)

			return 1
		}
	}

	return m.Run()
}

// Connect returns the emulator that Main started (or that GOPHERSTACK_ENDPOINT points to). The test is
// skipped if there is none, because Docker is not available.
func Connect(tb testing.TB) *Server {
	tb.Helper()
	endpoint := strings.TrimRight(os.Getenv(EnvEndpoint), "/")
	if endpoint == "" {
		tb.Skip("gopherstack is not available: Docker is not healthy and " + EnvEndpoint + " is not set")
	}

	return &Server{Endpoint: endpoint}
}

var nonDNS = regexp.MustCompile(`[^a-z0-9]+`)

// UniqueName returns a name unique to the test and the run (lowercase, DNS valid, <= 63 chars),
// usable as bucket, queue or stream name.
func UniqueName(tb testing.TB, prefix string) string {
	tb.Helper()
	base := strings.Trim(nonDNS.ReplaceAllString(strings.ToLower(tb.Name()), "-"), "-")
	if len(base) > maxNameBase {
		base = strings.Trim(base[:maxNameBase], "-")
	}
	suffix := fmt.Sprintf("%08x", randUint32())

	return strings.Trim(prefix+"-"+base, "-") + "-" + suffix
}
