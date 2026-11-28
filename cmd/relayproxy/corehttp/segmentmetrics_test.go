package corehttp_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/controlplane"
)

// series reads one series of a family of the metrics of the relay by one label; false when there is none.
func series(t *testing.T, rig *v2, family, label, value string) (float64, bool) {
	t.Helper()

	families, err := rig.metrics.Registry.Gather()
	require.NoError(t, err)

	for _, fam := range families {
		if fam.GetName() != family {
			continue
		}

		for _, metric := range fam.GetMetric() {
			if label == "" && len(metric.GetLabel()) == 0 {
				return metric.GetGauge().GetValue() + metric.GetCounter().GetValue(), true
			}

			for _, l := range metric.GetLabel() {
				if l.GetName() == label && l.GetValue() == value {
					return metric.GetGauge().GetValue() + metric.GetCounter().GetValue(), true
				}
			}
		}
	}

	return 0, false
}

// The dynamic segments are in /info and in the metrics: the state of each store, the served segments by health, and
// the counters of the resolvers, with no label of a flag set or a segment.
func TestE2E_DynamicSegmentsAreObservable(t *testing.T) {
	t.Parallel()

	rig := newV2With(t, &v2Options{connections: deadConnection(t), cold: true})
	rig.flags[dynFlagsObject] = []byte(dynFlagsYAML)
	rig.segments = map[string][]byte{segObjectName: []byte(segDocGood)}
	rig.sets = []controlplane.FlagSet{{
		Name: setName, Sources: []string{flagsObject, dynFlagsObject}, SegmentSources: []string{segObjectName},
		APIKeys: []string{tenantKey},
	}}

	rig.publish(t)
	rig.poll(t)

	cfg := infoOf(t, rig)
	require.Len(t, cfg.SegmentStores, 1)
	assert.Equal(t, "audiences", cfg.SegmentStores[0].Name)
	assert.Equal(t, "redis", cfg.SegmentStores[0].Kind)
	assert.Contains(t, []string{"connecting", "connected"}, cfg.SegmentStores[0].State)

	counts, _ := segmentsInfo(t, cfg)
	assert.Equal(t, &model.DynamicStatesInfo{Valid: 1, StoreError: 1}, counts.DynamicStates)

	for _, want := range []struct {
		family, label, value string
		wantValue            float64
	}{
		{"semaphore_segment_dynamic", "status", "store_error", 1},
		{"semaphore_segment_dynamic", "status", "healthy", 0},
		{"semaphore_segment_lookups_total", "result", "hit", 0},
		{"semaphore_segment_store_failed_calls_total", "", "", 0},
		{"semaphore_segment_breaker_transitions_total", "to", "open", 0},
	} {
		got, found := series(t, rig, want.family, want.label, want.value)
		assert.True(t, found, want.family+" "+want.value)
		assert.InDelta(t, want.wantValue, got, 0)
	}

	for _, state := range []string{"idle", "connecting", "connected", "error"} {
		_, found := series(t, rig, "semaphore_segment_store_state", "state", state)
		assert.True(t, found, state)
	}
}

// A snapshot segment that was never loaded is in /info and in the metrics: the held sets by connection and state, the
// members, and the shared bindings.
func TestE2E_SnapshotSegmentsAreObservable(t *testing.T) {
	t.Parallel()

	rig := newV2With(t, &v2Options{connections: deadConnection(t), cold: true})
	rig.flags[dynFlagsObject] = []byte(dynFlagsYAML)
	rig.segments = map[string][]byte{segObjectName: []byte(
		segHeaderYAML + "  - name: audience\n    connection: audiences\n")}
	rig.sets = []controlplane.FlagSet{{
		Name: setName, Sources: []string{flagsObject, dynFlagsObject}, SegmentSources: []string{segObjectName},
		APIKeys: []string{tenantKey},
	}}

	rig.publish(t)
	rig.poll(t)

	bindings := infoOf(t, rig).SegmentBindings
	require.NotNil(t, bindings)
	assert.Equal(t, 1, bindings.Live)
	assert.Equal(t, []model.SegmentSnapshotInfo{{Connection: "audiences", NeverLoaded: 1}}, bindings.Snapshots)

	for _, want := range []struct {
		family, label, value string
		wantValue            float64
	}{
		{"semaphore_segment_snapshot_sets", "state", "never_loaded", 1},
		{"semaphore_segment_snapshot_sets", "state", "loaded", 0},
		{"semaphore_segment_snapshot_members", "", "", 0},
		{"semaphore_segment_bindings", "", "", 1},
	} {
		got, found := series(t, rig, want.family, want.label, want.value)
		assert.True(t, found, want.family+" "+want.value)
		assert.InDelta(t, want.wantValue, got, 0)
	}
}
