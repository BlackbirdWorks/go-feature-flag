//go:build docker

package valkeysegment_test

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

const (
	clusterNodes    = 3
	clusterBasePort = 7000
)

// startCluster starts one container that runs three Valkey masters on fixed internal ports and joins them in one
// cluster. No port is chosen by the test and the container is on the default bridge network: the nodes announce
// their internal addresses, and the client dials the mapped port of the internal port that it is told, which is
// what the Dialer below does.
func startCluster(t *testing.T) *redis.ClusterClient {
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
	create = append(create, "--cluster-yes")

	code, out, err := node.Exec(ctx, create)
	require.NoError(t, err)

	body := new(strings.Builder)

	if out != nil {
		buf := make([]byte, 4096)
		n, _ := out.Read(buf)
		body.Write(buf[:n])
	}

	require.Zero(t, code, body.String())

	dialer := &net.Dialer{Timeout: 2 * time.Second}
	client := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:      []string{mapped[ports[0]]},
		MaxRetries: -1,
		// the nodes announce 127.0.0.1:<internal port>: dial the mapped address of that port
		Dialer: func(dialCtx context.Context, network, addr string) (net.Conn, error) {
			_, port, splitErr := net.SplitHostPort(addr)
			if splitErr != nil {
				return nil, fmt.Errorf("cluster address %q: %w", addr, splitErr)
			}

			target, known := mapped[port]
			if !known {
				target = addr
			}

			return dialer.DialContext(dialCtx, network, target)
		},
	})

	t.Cleanup(func() { _ = client.Close() })

	// every node agrees that the cluster is up and a write goes through
	require.Eventually(t, func() bool {
		var down atomic.Int32

		shardErr := client.ForEachShard(ctx, func(ctx context.Context, shard *redis.Client) error {
			info, infoErr := shard.ClusterInfo(ctx).Result()
			if infoErr != nil || !strings.Contains(info, "cluster_state:ok") {
				down.Add(1)
			}

			return nil
		})

		return shardErr == nil && down.Load() == 0 && client.Set(ctx, "cluster:probe", "1", 0).Err() == nil
	}, 60*time.Second, 100*time.Millisecond)

	return client
}

func TestE2ECluster(t *testing.T) {
	t.Parallel()

	client := startCluster(t)
	ctx := context.Background()

	names := []string{"alpha", segBeta, "gamma"}
	segs := map[string]valkeysegment.SegmentOptions{}

	for _, name := range names {
		segs[name] = valkeysegment.SegmentOptions{} // the default key has the hash tag
	}

	segs["flags"] = valkeysegment.SegmentOptions{Layout: valkeysegment.LayoutKeys, Key: flagPrefix}

	for _, name := range names {
		require.NoError(t, client.SAdd(ctx, "semaphore:segment:{"+name+"}", idAlice, name+"-only").Err())
	}

	require.NoError(t, client.Set(ctx, flagPrefix+idAlice, "1", 0).Err())
	require.NoError(t, client.Set(ctx, flagPrefix+"bob", "1", 0).Err())

	t.Run("a call over several segments and key layouts works across slots", func(t *testing.T) {
		t.Parallel()

		slots := map[uint16]bool{}
		for _, name := range names {
			slots[slotOf("semaphore:segment:{"+name+"}")] = true
		}

		require.Len(t, slots, len(names), "the segments are in different slots")

		for _, atomicTx := range []bool{false, true} {
			prov, err := valkeysegment.New(client, &valkeysegment.Options{Segments: segs, Atomic: atomicTx})
			require.NoError(t, err)

			lookups := []segment.Lookup{
				lk(
					"alpha",
					idAlice,
				),
				lk(segBeta, "zed"),
				lk("gamma", "gamma-only"),
				lk("flags", "bob"),
				lk("flags", "zed"),
				lk(segBeta, idAlice),
			}

			require.NoError(t, prov.Contains(ctx, lookups), "atomic=%v", atomicTx)
			assert.Equal(t, []segment.State{
				segment.Member, segment.NotMember, segment.Member, segment.Member, segment.NotMember, segment.Member,
			}, states(lookups), "atomic=%v", atomicTx)
		}
	})

	t.Run("the swap with a temporary key of the same tag stays in one slot", func(t *testing.T) {
		t.Parallel()

		live := "semaphore:segment:{swap}"
		prov, err := valkeysegment.New(client, &valkeysegment.Options{
			Atomic: true, Timeout: 5 * time.Second,
			Segments: map[string]valkeysegment.SegmentOptions{"swap": {}},
		})
		require.NoError(t, err)

		require.NoError(t, client.SAdd(ctx, live, "stable").Err())

		for i := range 20 {
			tmp := live + ":tmp"

			require.NoError(t, client.SAdd(ctx, tmp, "stable", "gen"+strconv.Itoa(i)).Err())
			require.NoError(t, client.Rename(ctx, tmp, live).Err(), "RENAME in one slot")

			lookups := []segment.Lookup{lk("swap", "stable"), lk("swap", "absent")}

			require.NoError(t, prov.Contains(ctx, lookups))
			assert.Equal(t, []segment.State{segment.Member, segment.NotMember}, states(lookups))
		}
	})

	t.Run("a temporary key without the tag cannot be renamed onto the set", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, client.SAdd(ctx, "semaphore:segment:{cross}", "a").Err())
		require.NoError(t, client.SAdd(ctx, "semaphore:segment:cross:tmp", "a").Err())

		err := client.Rename(ctx, "semaphore:segment:cross:tmp", "semaphore:segment:{cross}").Err()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "CROSSSLOT")
	})
}
