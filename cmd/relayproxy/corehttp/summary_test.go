package corehttp_test

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/controlsource"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
)

const summariesMetric = "semaphore_config_summaries_total"

// gatedStatus holds each of the first want readers inside Status until all of them arrived, so that they all meet
// the same runtime before any summary exists.
type gatedStatus struct {
	gate    chan struct{}
	status  relaycore.Status
	arrived atomic.Int64
	want    int64
}

func (g *gatedStatus) Status() relaycore.Status {
	if g.arrived.Add(1) == g.want {
		close(g.gate)
	}

	select {
	case <-g.gate:
	case <-time.After(waitBound):
	}

	return g.status
}

// summariesBuilt reads the counter of the summaries from the registry of the collector.
func summariesBuilt(t *testing.T, col prom.Collector) float64 {
	t.Helper()

	reg := prom.NewPedanticRegistry()
	require.NoError(t, reg.Register(col))

	families, err := reg.Gather()
	require.NoError(t, err)

	for _, fam := range families {
		if fam.GetName() == summariesMetric {
			return fam.GetMetric()[0].GetCounter().GetValue()
		}
	}

	return -1
}

// TestNewConfigCollectorSharing_Concurrent: readiness probes and metric scrapes that miss the cache of the same
// runtime together wait for one build.
func TestNewConfigCollectorSharing_Concurrent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		probes    int
		scrapes   int
		wantBuilt float64
	}{
		{name: "probe and scrape", probes: 1, scrapes: 1, wantBuilt: 1},
		{name: "many readers", probes: 8, scrapes: 8, wantBuilt: 1},
		{name: "probes only", probes: 4, scrapes: 0, wantBuilt: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := &gatedStatus{
				status: relaycore.Status{Source: controlsource.Status{Runtime: &flagruntime.Status{
					Snapshot: 1, Generation: 1,
					FlagSets: manySets(),
				}}},
				gate: make(chan struct{}),
				want: int64(tt.probes + tt.scrapes),
			}
			ready := corehttp.NewReadiness(src)
			col := corehttp.NewConfigCollectorSharing(src, ready)
			readers := tt.probes + tt.scrapes

			var wg sync.WaitGroup

			for i := range readers {
				wg.Go(func() {
					if i < tt.probes {
						ready.Ready()

						return
					}

					out := make(chan prom.Metric, 64)
					col.Collect(out)
				})
			}

			wg.Wait()

			assert.InDelta(t, tt.wantBuilt, summariesBuilt(t, col), 0)
		})
	}
}

// manySets is a runtime big enough that a build takes long enough for concurrent readers to overlap.
func manySets() map[string]*flagruntime.FlagSetStatus {
	sets := make(map[string]*flagruntime.FlagSetStatus, 50000)

	for i := range 50000 {
		name := "set-" + strconv.Itoa(i)
		sets[name] = &flagruntime.FlagSetStatus{Name: name, State: flagruntime.StateServing}
	}

	return sets
}
