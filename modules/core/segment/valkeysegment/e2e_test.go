//go:build docker

package valkeysegment_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	testcontainerredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

const (
	valkeyImage = "docker.io/valkey/valkey:8-alpine"
	redisImage  = "docker.io/redis:7-alpine"

	clusterNodes    = 3
	clusterBasePort = 7000
	e2eTimeout      = 5 * time.Second
)

// e2eServer is a real store and the options of a client of it that counts the writes of its connections.
type e2eServer struct {
	container *testcontainerredis.RedisContainer
	writes    *atomic.Int64
	address   string
}

func startServerImage(tb testing.TB, image string) *e2eServer {
	tb.Helper()

	ctx := context.Background()

	container, err := testcontainerredis.Run(ctx, image)
	testcontainers.CleanupContainer(tb, container)
	require.NoError(tb, err)

	address, err := container.Endpoint(ctx, "")
	require.NoError(tb, err)

	return &e2eServer{container: container, address: address, writes: &atomic.Int64{}}
}

func (s *e2eServer) option(dial func(context.Context, string, *net.Dialer) (net.Conn, error)) *valkey.ClientOption {
	return &valkey.ClientOption{
		InitAddress:       []string{s.address},
		DisableCache:      true,
		DisableRetry:      true,
		PipelineMultiplex: -1, // one connection: the setup writes of a multiplex would count as calls
		DialCtxFn: func(ctx context.Context, addr string, dialer *net.Dialer, _ *tls.Config) (net.Conn, error) {
			conn, err := dial(ctx, addr, dialer)
			if err != nil {
				return nil, err
			}

			return &countingConn{Conn: conn, writes: s.writes}, nil
		},
	}
}

func plainDial(ctx context.Context, addr string, dialer *net.Dialer) (net.Conn, error) {
	return dialer.DialContext(ctx, "tcp", addr)
}

// seed returns a client for loading the data of a test, closed at the end of it.
//
//nolint:ireturn // the valkey-go client is an interface
func seed(tb testing.TB, opt *valkey.ClientOption) valkey.Client {
	tb.Helper()

	plain := *opt
	plain.DialCtxFn = nil

	client, err := valkey.NewClient(plain)
	require.NoError(tb, err)
	tb.Cleanup(client.Close)

	return client
}

func load(tb testing.TB, client valkey.Client, set string, ids ...string) {
	tb.Helper()

	require.NoError(tb, client.Do(context.Background(), client.B().Sadd().Key(set).Member(ids...).Build()).Error())
}

func loadKey(tb testing.TB, client valkey.Client, key string) {
	tb.Helper()

	require.NoError(tb, client.Do(context.Background(), client.B().Set().Key(key).Value("1").Build()).Error())
}

// calls returns the calls that the server counted for the command, from INFO commandstats.
func calls(tb testing.TB, client valkey.Client, command string) int {
	tb.Helper()

	info, err := client.Do(context.Background(), client.B().Info().Section("commandstats").Build()).ToString()
	require.NoError(tb, err)

	for line := range strings.SplitSeq(info, "\r\n") {
		if rest, found := strings.CutPrefix(line, "cmdstat_"+command+":calls="); found {
			count, _, _ := strings.Cut(rest, ",")
			val, convErr := strconv.Atoi(count)
			require.NoError(tb, convErr)

			return val
		}
	}

	return 0
}

func e2eProvider(tb testing.TB, opt *valkey.ClientOption, opts *valkeysegment.Options) *valkeysegment.Provider {
	tb.Helper()

	if opts == nil {
		opts = &valkeysegment.Options{}
	}

	if opts.Segments == nil {
		opts.Segments = map[string]valkeysegment.SegmentOptions{
			segBeta: {Key: keyBeta}, segFlags: {Layout: valkeysegment.LayoutKeys, Key: flagPrefix},
		}
	}

	prov, err := valkeysegment.Dial(opt, opts)
	require.NoError(tb, err)
	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownBound)
		defer cancel()

		assert.NoError(tb, prov.Shutdown(ctx))
	})

	return prov
}

// TestE2EContains runs on Valkey and on Redis: the store is whatever answers SMISMEMBER and EXISTS.
func TestE2EContains(t *testing.T) {
	t.Parallel()

	for _, image := range []string{valkeyImage, redisImage} {
		t.Run(image, func(t *testing.T) {
			t.Parallel()

			srv := startServerImage(t, image)
			opt := srv.option(plainDial)
			client := seed(t, opt)

			load(t, client, keyBeta, idAlice, idCarol)
			loadKey(t, client, flagPrefix+idAlice)

			tests := []struct {
				name       string
				opts       *valkeysegment.Options
				lookups    []segment.Lookup
				wantStates []segment.State
			}{
				{
					name:       "member and not member",
					lookups:    []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")},
					wantStates: []segment.State{segment.Member, segment.NotMember},
				},
				{
					name:    "missing key is unknown",
					lookups: []segment.Lookup{lk(segGhost, idAlice)},
					opts: &valkeysegment.Options{
						Segments: map[string]valkeysegment.SegmentOptions{segGhost: {}},
					},
					wantStates: []segment.State{segment.Unknown},
				},
				{
					name:    "missing key is not member on request",
					lookups: []segment.Lookup{lk(segGhost, idAlice)},
					opts: &valkeysegment.Options{
						MissingKey: valkeysegment.MissingNotMember,
						Segments:   map[string]valkeysegment.SegmentOptions{segGhost: {}},
					},
					wantStates: []segment.State{segment.NotMember},
				},
				{
					name:       nameKeysLayout,
					lookups:    []segment.Lookup{lk(segFlags, idAlice), lk(segFlags, "zed")},
					wantStates: []segment.State{segment.Member, segment.NotMember},
				},
				{
					name:       "set and keys in one call",
					lookups:    []segment.Lookup{lk(segFlags, idAlice), lk(segBeta, idCarol), lk(segBeta, "x")},
					wantStates: []segment.State{segment.Member, segment.Member, segment.NotMember},
				},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()

					prov := e2eProvider(t, opt, tt.opts)

					require.NoError(t, prov.Init(context.Background()))
					require.NoError(t, prov.Contains(context.Background(), tt.lookups))
					assert.Equal(t, tt.wantStates, states(tt.lookups))
				})
			}
		})
	}
}

func TestE2EOneRoundTrip(t *testing.T) {
	t.Parallel()

	srv := startServerImage(t, valkeyImage)
	opt := srv.option(plainDial)
	client := seed(t, opt)

	load(t, client, keyBeta, "id3")

	lookups := make([]segment.Lookup, 8)
	for i := range lookups {
		lookups[i] = lk(segBeta, "id"+strconv.Itoa(i))
	}

	prov := e2eProvider(t, opt, &valkeysegment.Options{Timeout: e2eTimeout})

	// the first use of a connection writes its setup: warm it before counting
	require.NoError(t, prov.Init(context.Background()))

	beforeWrites := srv.writes.Load()
	beforeSMIs := calls(t, client, "smismember")
	beforeExists := calls(t, client, "exists")

	require.NoError(t, prov.Contains(context.Background(), lookups))

	assert.Equal(t, int64(1), srv.writes.Load()-beforeWrites, "one write of the client for the whole call")
	assert.Equal(t, 1, calls(t, client, "smismember")-beforeSMIs, "one SMISMEMBER for the eight ids")
	assert.Equal(t, 1, calls(t, client, "exists")-beforeExists, "one key check")

	want := make([]segment.State, len(lookups))
	for i := range want {
		want[i] = segment.NotMember
	}

	want[3] = segment.Member

	assert.Equal(t, want, states(lookups))
}

func TestE2EServerStopped(t *testing.T) {
	t.Parallel()

	srv := startServerImage(t, valkeyImage)
	opt := srv.option(plainDial)
	load(t, seed(t, opt), keyBeta, idAlice)

	prov := e2eProvider(t, opt, &valkeysegment.Options{Timeout: 2 * time.Second})
	before := []segment.Lookup{lk(segBeta, idAlice)}

	require.NoError(t, prov.Contains(context.Background(), before))
	require.Equal(t, []segment.State{segment.Member}, states(before))
	require.NoError(t, srv.container.Stop(context.Background(), nil))

	lookups := []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")}
	err := prov.Contains(context.Background(), lookups)

	var lookupErr *valkeysegment.LookupError

	require.ErrorAs(t, err, &lookupErr)
	assert.Contains(
		t,
		[]valkeysegment.Class{valkeysegment.ClassUnavailable, valkeysegment.ClassTimeout},
		lookupErr.Class,
	)
	assert.Equal(t, 2, lookupErr.Failed)
	assert.Equal(t, []segment.State{segment.Unknown, segment.Unknown}, states(lookups))
}

// startCluster starts one container that runs three Valkey masters on fixed internal ports and joins them in one
// cluster. The nodes announce their internal addresses, so the client dials the mapped port of the internal port
// that it is told.
func startCluster(t *testing.T) *valkey.ClientOption {
	t.Helper()

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
		Image:        valkeyImage,
		Entrypoint:   []string{"sh", "-c"},
		Cmd:          []string{script.String()},
		ExposedPorts: exposed,
		WaitingFor:   wait.ForLog("Ready to accept connections").WithOccurrence(clusterNodes),
		Started:      true,
	})
	testcontainers.CleanupContainer(t, node)
	require.NoError(t, err)

	host, err := node.Host(ctx)
	require.NoError(t, err)

	mapped := map[string]string{} // the internal port to the address that the host reaches it on

	for _, port := range ports {
		hostPort, portErr := node.MappedPort(ctx, port+"/tcp")
		require.NoError(t, portErr)

		mapped[port] = net.JoinHostPort(host, hostPort.Port())
	}

	members := make([]string, clusterNodes)
	for i, port := range ports {
		members[i] = "127.0.0.1:" + port
	}

	create := append([]string{"valkey-cli", "-p", ports[0], "--cluster", "create"}, members...)
	code, out, err := node.Exec(ctx, append(create, "--cluster-yes"))
	require.NoError(t, err)

	body := new(strings.Builder)

	if out != nil {
		buf := make([]byte, 4096)
		n, _ := out.Read(buf)
		body.Write(buf[:n])
	}

	require.Zero(t, code, body.String())

	return &valkey.ClientOption{
		InitAddress:  []string{mapped[ports[0]]},
		DisableCache: true,
		DisableRetry: true,
		DialCtxFn: func(dialCtx context.Context, addr string, dialer *net.Dialer, _ *tls.Config) (net.Conn, error) {
			_, port, splitErr := net.SplitHostPort(addr)
			if splitErr != nil {
				return nil, fmt.Errorf("cluster address %q: %w", addr, splitErr)
			}

			target, known := mapped[port]
			if !known {
				target = addr
			}

			return plainDial(dialCtx, target, dialer)
		},
	}
}

func TestE2ECluster(t *testing.T) {
	t.Parallel()

	opt := startCluster(t)
	names := []string{"alpha", segBeta, "gamma"}
	segs := map[string]valkeysegment.SegmentOptions{segFlags: {Layout: valkeysegment.LayoutKeys, Key: flagPrefix}}

	var client valkey.Client

	// the cluster accepts clients once every node agrees that it is up
	require.Eventually(t, func() bool {
		var err error

		client, err = valkey.NewClient(*opt)

		return err == nil
	}, 60*time.Second, 200*time.Millisecond)
	t.Cleanup(client.Close)

	require.Eventually(t, func() bool {
		return client.Do(context.Background(), client.B().Set().Key("cluster:probe").Value("1").Build()).Error() == nil
	}, 60*time.Second, 200*time.Millisecond)

	for _, name := range names {
		segs[name] = valkeysegment.SegmentOptions{} // the default key has the hash tag
		load(t, client, "semaphore:segment:{"+name+"}", idAlice, name+"-only")
	}

	loadKey(t, client, flagPrefix+idAlice)
	loadKey(t, client, flagPrefix+"bob")

	prov := e2eProvider(t, opt, &valkeysegment.Options{Segments: segs, Timeout: e2eTimeout})

	require.NoError(t, prov.Init(context.Background()))

	lookups := []segment.Lookup{
		lk("alpha", idAlice), lk(segBeta, "zed"), lk("gamma", "gamma-only"),
		lk(segFlags, "bob"), lk(segFlags, "zed"), lk(segBeta, idAlice),
	}

	require.NoError(t, prov.Contains(context.Background(), lookups))
	assert.Equal(t, []segment.State{
		segment.Member, segment.NotMember, segment.Member, segment.Member, segment.NotMember, segment.Member,
	}, states(lookups))

	got, err := prov.Exists(context.Background(), []string{"alpha", segBeta, "gamma", segFlags, segNope})
	require.NoError(t, err)
	assert.Equal(t, []segment.State{
		segment.Member, segment.Member, segment.Member, segment.Unknown, segment.Unknown,
	}, got)
}
