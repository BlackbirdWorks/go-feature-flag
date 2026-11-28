//go:build docker

package segmenttest

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	testcontainerredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/valkey-io/valkey-go"
)

// Images of the servers that the docker tests start.
const (
	ValkeyImage = "docker.io/valkey/valkey:8-alpine"
	RedisImage  = "docker.io/redis:7-alpine"

	clusterNodes    = 3
	clusterBasePort = 7000
	dialTimeout     = 2 * time.Second
)

// Server is a store that a container runs: one node, or three masters of a cluster. The nodes of the cluster announce
// their internal addresses, so a client dials through [Server.Dial], which maps them to the ports of the host.
type Server struct {
	mapped  map[string]string
	Addrs   []string
	Cluster bool
}

// Dial connects to an address that the server announced.
func (s *Server) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	target := addr

	_, port, err := net.SplitHostPort(addr)
	if host, ok := s.mapped[port]; err == nil && ok {
		target = host
	}

	dialer := &net.Dialer{Timeout: dialTimeout}

	conn, err := dialer.DialContext(ctx, network, target)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", target, err)
	}

	return conn, nil
}

// ValkeyOption is the valkey-go option of a client of the server. Retries are off, so that a failure shows.
func (s *Server) ValkeyOption() valkey.ClientOption {
	return valkey.ClientOption{
		InitAddress: s.Addrs, DisableCache: true, DisableRetry: true, ForceSingleClient: !s.Cluster,
		AlwaysPipelining: true,
		DialCtxFn: func(ctx context.Context, addr string, _ *net.Dialer, _ *tls.Config) (net.Conn, error) {
			return s.Dial(ctx, "tcp", addr)
		},
	}
}

// RedisOptions is the go-redis option of a client of the server (a cluster client when the server is a cluster).
func (s *Server) RedisOptions() *redis.UniversalOptions {
	return &redis.UniversalOptions{
		Addrs: s.Addrs, IsClusterMode: s.Cluster, ContextTimeoutEnabled: true, MaxRetries: -1,
		Dialer: s.Dial,
	}
}

// StartServer starts one node of an image.
func StartServer(tb testing.TB, image string) *Server {
	tb.Helper()

	ctx := context.Background()

	container, err := testcontainerredis.Run(ctx, image)
	testcontainers.CleanupContainer(tb, container)
	require.NoError(tb, err)

	address, err := container.Endpoint(ctx, "")
	require.NoError(tb, err)

	return &Server{Addrs: []string{address}, mapped: map[string]string{}}
}

// StartCluster starts one container that runs three Valkey masters on fixed internal ports, joins them in a cluster and
// waits until every node agrees that it is ready.
func StartCluster(tb testing.TB) *Server {
	tb.Helper()

	ctx := context.Background()
	ports := make([]string, clusterNodes)
	exposed := make([]string, clusterNodes)
	script := new(strings.Builder)

	for i := range clusterNodes {
		ports[i] = strconv.Itoa(clusterBasePort + i)
		exposed[i] = ports[i] + "/tcp"

		script.WriteString(
			"valkey-server --port " + ports[i] + " --cluster-enabled yes --cluster-config-file /tmp/nodes-" +
				ports[i] + ".conf --cluster-node-timeout 5000 --appendonly no --save '' & ",
		)
	}

	script.WriteString("wait")

	node, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		Image: ValkeyImage, Entrypoint: []string{"sh", "-c"}, Cmd: []string{script.String()}, ExposedPorts: exposed,
		WaitingFor: wait.ForLog("Ready to accept connections").WithOccurrence(clusterNodes), Started: true,
	})
	testcontainers.CleanupContainer(tb, node)
	require.NoError(tb, err)

	host, err := node.Host(ctx)
	require.NoError(tb, err)

	srv := &Server{mapped: map[string]string{}, Cluster: true}

	for _, port := range ports {
		hostPort, portErr := node.MappedPort(ctx, port+"/tcp")
		require.NoError(tb, portErr)

		srv.mapped[port] = net.JoinHostPort(host, hostPort.Port())
	}

	srv.Addrs = []string{srv.mapped[ports[0]]}

	members := make([]string, clusterNodes)
	for i, port := range ports {
		members[i] = "127.0.0.1:" + port
	}

	create := append([]string{"valkey-cli", "-p", ports[0], "--cluster", "create"}, members...)
	code, out, err := node.Exec(ctx, append(create, "--cluster-yes"))
	require.NoError(tb, err)

	body, _ := io.ReadAll(out)
	require.Zero(tb, code, string(body))

	AwaitClusterReady(tb, ports, func(cmd []string) (string, error) {
		_, reply, execErr := node.Exec(ctx, cmd)
		if execErr != nil {
			return "", fmt.Errorf("cluster info: %w", execErr)
		}

		buf, readErr := io.ReadAll(reply)
		if readErr != nil {
			return "", fmt.Errorf("cluster info: %w", readErr)
		}

		return string(buf), nil
	})

	return srv
}
