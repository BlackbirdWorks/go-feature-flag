package corehttp

import (
	"fmt"
	"sync/atomic"
	"testing"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/controlsource"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
)

type staticStatus struct {
	st atomic.Pointer[relaycore.Status]
}

func (s *staticStatus) Status() relaycore.Status { return *s.st.Load() }

// bigStatus is a runtime of n flag sets with flagsPerSet flags each; every 500th flag set is degraded.
func bigStatus(n, flagsPerSet int) *flagruntime.Status {
	st := &flagruntime.Status{
		Snapshot: 2, Generation: 1,
		FlagSets: make(map[string]*flagruntime.FlagSetStatus, n),
		Objects:  map[string]*flagruntime.ObjectStatus{"o": {Name: "o", State: flagruntime.ObjectOK}},
	}

	for i := range n {
		state := flagruntime.StateServing
		if i%500 == 0 {
			state = flagruntime.StateDegraded
		}

		name := fmt.Sprintf("set-%05d", i)
		st.FlagSets[name] = &flagruntime.FlagSetStatus{Name: name, State: state, Flags: flagsPerSet}
	}

	return st
}

func collector(rt *flagruntime.Status) (*ConfigCollector, *staticStatus) {
	src := &staticStatus{}
	src.st.Store(&relaycore.Status{Source: controlsource.Status{Runtime: rt}})

	return NewConfigCollector(src), src
}

// TestConfigCollector_ScrapeCost pins that the series of a scrape follow the problems and never the flags, and that a
// runtime is summarised once however often it is scraped.
func TestConfigCollector_ScrapeCost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		sets        int
		flagsPerSet int
		wantSeries  int
	}{
		{name: "one flag per set", sets: 5000, flagsPerSet: 1, wantSeries: 26},
		{name: "thousand flags per set", sets: 5000, flagsPerSet: 1000, wantSeries: 26},
		{name: "no flag sets", sets: 0, flagsPerSet: 0, wantSeries: 16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			col, _ := collector(bigStatus(tt.sets, tt.flagsPerSet))

			reg := prom.NewPedanticRegistry()
			require.NoError(t, reg.Register(col))

			// 5000 sets: ten are degraded, so ten named series
			assert.Equal(t, tt.wantSeries, testutil.CollectAndCount(col))

			for range 3 {
				_, err := reg.Gather()
				require.NoError(t, err)
			}

			assert.Equal(t, uint64(1), col.sums.computed.Load())
		})
	}
}

// TestConfigCollector_NamedSeriesAreCapped pins the cap on the series by name.
func TestConfigCollector_NamedSeriesAreCapped(t *testing.T) {
	t.Parallel()

	rt := bigStatus(1, 1)
	for i := range 300 {
		name := fmt.Sprintf("bad-%03d", i)
		rt.FlagSets[name] = &flagruntime.FlagSetStatus{Name: name, State: flagruntime.StatePending}
	}

	col, _ := collector(rt)

	// 5 gauges + 3 reloads + 3 flag set counts + 5 object counts = 16, plus the cap for the degraded flag sets
	assert.Equal(t, 16+maxNamedResources, testutil.CollectAndCount(col))
}

// TestConfigCollector_NoRuntime pins the series before any runtime exists.
func TestConfigCollector_NoRuntime(t *testing.T) {
	t.Parallel()

	col, _ := collector(nil)

	assert.Equal(t, 16, testutil.CollectAndCount(col))
}
