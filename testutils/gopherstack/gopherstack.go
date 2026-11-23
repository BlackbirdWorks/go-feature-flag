// Package gopherstack starts the gopherstack AWS emulator (https://github.com/blackbirdworks/gopherstack)
// in a Docker container, to run end-to-end tests of the AWS-backed components (S3, Kinesis, SQS).
//
// One emulator is shared by all the tests of a package: use Shared to get it, and TerminateShared
// from a TestMain if you want the container removed before the process exits (the testcontainers
// reaper removes it anyway).
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
	"sync"
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
	// Endpoint is the base URL (ex: http://127.0.0.1:49153). An IP host makes the AWS SDK use
	// path-style S3 addressing, so no DNS is needed.
	Endpoint  string
	container testcontainers.Container
}

// Start starts a container (or attaches to GOPHERSTACK_ENDPOINT) and waits until it is healthy.
func Start(ctx context.Context) (*Server, error) {
	if ep := strings.TrimRight(os.Getenv(EnvEndpoint), "/"); ep != "" {
		if err := waitHealthy(ctx, ep); err != nil {
			return nil, err
		}
		return &Server{Endpoint: ep}, nil
	}
	image := Image
	if v := os.Getenv(EnvImage); v != "" {
		image = v
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        image,
			ExposedPorts: []string{Port},
			WaitingFor: wait.ForHTTP(HealthPath).
				WithPort(Port).
				WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		if c != nil {
			_ = testcontainers.TerminateContainer(c)
		}
		return nil, fmt.Errorf("start gopherstack: %w", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return nil, err
	}
	if host == "localhost" {
		host = "127.0.0.1" // keep an IP host for path-style S3
	}
	port, err := c.MappedPort(ctx, Port)
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return nil, err
	}
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(host, port.Port())}
	return &Server{Endpoint: u.String(), container: c}, nil
}

func waitHealthy(ctx context.Context, endpoint string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for {
		//nolint:gosec // own container or GOPHERSTACK_ENDPOINT
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+HealthPath, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req) //nolint:gosec // same endpoint
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("gopherstack at %s is not healthy: %w", endpoint, ctx.Err())
		case <-time.After(200 * time.Millisecond):
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
func (s *Server) MustAwsConfig(t testing.TB) aws.Config {
	t.Helper()
	cfg, err := s.AwsConfig(t.Context())
	require.NoError(t, err)
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

var (
	sharedMu sync.Mutex
	shared   *Server
	// sharedStart caches the error of a failed start, so it is not retried by every test.
	sharedStart struct{ err error }
)

// Shared returns the emulator of the test binary, started on the first call. The test is skipped
// if Docker is not available, and fails if the container can't be started.
func Shared(t *testing.T) *Server {
	t.Helper()
	if os.Getenv(EnvEndpoint) == "" {
		testcontainers.SkipIfProviderIsNotHealthy(t)
	}
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if shared == nil && sharedStart.err == nil {
		shared, sharedStart.err = Start(context.Background())
	}
	require.NoError(t, sharedStart.err)
	return shared
}

// TerminateShared removes the shared container, to call from a TestMain after m.Run.
func TerminateShared() {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if shared != nil {
		_ = shared.Terminate()
		shared = nil
	}
}

var nonDNS = regexp.MustCompile(`[^a-z0-9]+`)

// UniqueName returns a name unique to the test and the run (lowercase, DNS valid, <= 63 chars),
// usable as bucket, queue or stream name.
func UniqueName(t testing.TB, prefix string) string {
	t.Helper()
	base := strings.Trim(nonDNS.ReplaceAllString(strings.ToLower(t.Name()), "-"), "-")
	if len(base) > 30 {
		base = strings.Trim(base[:30], "-")
	}
	suffix := fmt.Sprintf("%08x", randUint32())
	return strings.Trim(prefix+"-"+base, "-") + "-" + suffix
}
