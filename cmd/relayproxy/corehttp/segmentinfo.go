package corehttp

import (
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
)

// addSegmentInfo adds the live state of the dynamic segments to the info of the configuration: the state of each
// store, and for each flag set the served segments by health. No store is called.
func addSegmentInfo(info *model.ConfigInfo, segments *relaycore.SegmentsStatus) {
	for _, store := range segments.Stores {
		info.SegmentStores = append(info.SegmentStores,
			model.SegmentStoreInfo{Name: store.Name, Kind: store.Kind, State: store.State})
	}

	if segments.Bindings.Live > 0 {
		bindings := &model.SegmentBindingsInfo{Live: segments.Bindings.Live}

		for _, conn := range segments.Bindings.Connections {
			bindings.Snapshots = append(bindings.Snapshots, model.SegmentSnapshotInfo{
				Connection: conn.Connection, Loaded: conn.Loaded, Stale: conn.Stale, Missing: conn.Missing,
				NeverLoaded: conn.NeverLoaded, Members: conn.Members, Unsettled: conn.Unsettled,
			})
		}

		info.SegmentBindings = bindings
	}

	for i := range info.FlagSets {
		set := &info.FlagSets[i]

		health, ok := segments.Sets[set.Name]
		if !ok || set.Segments == nil || set.Segments.DynamicStates == nil {
			continue
		}

		set.Segments.DynamicStates.Healthy, set.Segments.DynamicStates.StoreError = health.Healthy, health.StoreError
	}
}
