package segmenttest

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	clusterReadyWithin = 60 * time.Second
	clusterReadyPoll   = 200 * time.Millisecond
	clusterSlots       = 16384
)

// lastClusterInfo keeps the last CLUSTER INFO of each node, for the message of a cluster that does not become ready.
type lastClusterInfo struct {
	byPort map[string]string
	mu     sync.Mutex
}

// String implements [fmt.Stringer].
func (l *lastClusterInfo) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	var out strings.Builder

	for port, info := range l.byPort {
		fmt.Fprintf(&out, "\nnode %s:\n%s", port, info)
	}

	return out.String()
}

func (l *lastClusterInfo) set(port, info string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.byPort[port] = info
}

// AwaitClusterReady waits until every node of a cluster that was just created agrees that it is usable: CLUSTER INFO
// says cluster_state:ok, all 16384 slots assigned, and as many known nodes as there are ports. A command that is sent
// before that, to any node, can be answered CLUSTERDOWN. run executes a command in the container of the nodes and
// returns its output. The failure message holds the last CLUSTER INFO of each node.
func AwaitClusterReady(tb testing.TB, ports []string, run func(cmd []string) (string, error)) {
	tb.Helper()

	last := &lastClusterInfo{byPort: map[string]string{}}
	wants := []string{
		"cluster_state:ok", fmt.Sprintf("cluster_slots_assigned:%d", clusterSlots),
		fmt.Sprintf("cluster_known_nodes:%d", len(ports)),
	}

	require.Eventually(tb, func() bool {
		ready := true

		for _, port := range ports {
			info, err := run([]string{"valkey-cli", "-p", port, "cluster", "info"})
			if err != nil {
				info = "error: " + err.Error()
			}

			last.set(port, info)

			for _, want := range wants {
				if !strings.Contains(info, want) {
					ready = false
				}
			}
		}

		return ready
	}, clusterReadyWithin, clusterReadyPoll, "the cluster is not ready (want %s): %s", strings.Join(wants, ", "), last)
}
