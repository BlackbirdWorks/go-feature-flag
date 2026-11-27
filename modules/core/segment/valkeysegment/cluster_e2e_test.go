//go:build docker

package valkeysegment_test

import (
	"context"
	"net"
	"strconv"
	"strings"
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

const clusterNodes = 3

// freePorts returns ports that were free a moment ago. The nodes of a cluster announce their own ports, so they
// cannot be mapped to random ones.
func freePorts(t *testing.T, count int) []int {
	t.Helper()

	listeners := make([]net.Listener, count)
	ports := make([]int, count)
	lc := net.ListenConfig{}

	for i := range count {
		listener, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)

		listeners[i] = listener
		addr, isTCP := listener.Addr().(*net.TCPAddr)
		require.True(t, isTCP)

		ports[i] = addr.Port
	}

	for _, listener := range listeners {
		require.NoError(t, listener.Close())
	}

	return ports
}

// startCluster starts three Valkey masters on the host network and joins them in one cluster.
func startCluster(t *testing.T) *redis.ClusterClient {
	t.Helper()

	ctx := context.Background()
	ports := freePorts(t, clusterNodes)
	addrs := make([]string, clusterNodes)

	var first testcontainers.Container

	for i, port := range ports {
		addrs[i] = "127.0.0.1:" + strconv.Itoa(port)

		node, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			Image: valkeyImage,
			Cmd: []string{
				"valkey-server", "--port", strconv.Itoa(port), "--cluster-enabled", "yes",
				"--cluster-config-file", "/tmp/nodes.conf", "--cluster-node-timeout", "5000",
				"--cluster-announce-ip", "127.0.0.1", "--appendonly", "no", "--save", "",
			},
			NetworkMode: "host",
			WaitingFor:  wait.ForLog("Ready to accept connections"),
			Started:     true,
		})
		testcontainers.CleanupContainer(t, node)
		require.NoError(t, err)

		if i == 0 {
			first = node
		}
	}

	create := append([]string{"valkey-cli", "-p", strconv.Itoa(ports[0]), "--cluster", "create"}, addrs...)
	create = append(create, "--cluster-yes")

	code, out, err := first.Exec(ctx, create)
	require.NoError(t, err)

	body := new(strings.Builder)

	if out != nil {
		buf := make([]byte, 4096)
		n, _ := out.Read(buf)
		body.Write(buf[:n])
	}

	require.Zero(t, code, body.String())

	client := redis.NewClusterClient(&redis.ClusterOptions{Addrs: addrs, MaxRetries: -1, DialTimeout: 2 * time.Second})

	t.Cleanup(func() { _ = client.Close() })

	require.Eventually(t, func() bool {
		info, infoErr := client.ClusterInfo(ctx).Result()

		return infoErr == nil && strings.Contains(info, "cluster_state:ok")
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
			prov, err := valkeysegment.New(client, valkeysegment.Options{Segments: segs, Atomic: atomicTx})
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
		prov, err := valkeysegment.New(client, valkeysegment.Options{
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
