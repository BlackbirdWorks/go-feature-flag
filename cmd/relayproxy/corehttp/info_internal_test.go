package corehttp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/controlsource"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

// TestConfigInfo pins the objects of /info for the states the e2e scenarios cannot reach: a stale object keeps its held
// checksum, shows the listed one and the closed reason; free text never appears.
const (
	staleName = "stale-obj"
	kindFlags = "flags"
	fineName  = "fine"
	sameSum   = "same"
)

func TestConfigInfo(t *testing.T) {
	t.Parallel()

	now := clock.Now()
	synced := now.Add(-90 * time.Second)

	status := relaycore.Status{
		Reloads: relaycore.ReloadStats{Applied: 2, LastSuccess: synced},
		Source: controlsource.Status{
			Reader: manifestreader.Status{
				LastSync: synced,
				Failures: []manifestreader.ObjectFailure{
					{Name: staleName, State: manifestreader.StateStale, Reason: "timeout", Checksum: "listed"},
				},
			},
			Runtime: &flagruntime.Status{
				Generation: 4, Snapshot: 5, PendingRestart: []string{"exporters"},
				Objects: map[string]*flagruntime.ObjectStatus{
					staleName: {Name: staleName, Kind: kindFlags, State: flagruntime.ObjectStale, Checksum: "held",
						Reason: "secret text", Flags: 3},
					fineName: {Name: fineName, Kind: kindFlags, State: flagruntime.ObjectOK, Checksum: sameSum},
				},
				FlagSets: map[string]*flagruntime.FlagSetStatus{
					"b": {Name: "b", State: flagruntime.StateDegraded, APIKeys: 2, Problems: []flagruntime.Problem{
						{Code: flagruntime.CodeSourceDegraded, Subject: staleName, Reason: "secret text"},
					}},
					"a": {Name: "a", State: flagruntime.StateServing, Flags: 3},
				},
				Withdrawn: []flagruntime.WithdrawnSet{{Name: "w", Generation: 4}},
			},
		},
	}

	want := model.ConfigInfo{
		LastReload: model.ReloadInfo{},
		Staleness:  model.StalenessInfo{LastConfirmed: &synced, LastSuccessfulReload: &synced, AgeSeconds: 90},
		Reloads:    map[string]uint64{"applied": 2, "rejected": 0, "ignored": 0},
		FlagSets: []model.FlagSetInfo{
			{Name: "a", State: "ready", Problems: []model.ProblemInfo{}, Flags: 3},
			{Name: "b", State: "degraded", APIKeys: 2,
				Problems: []model.ProblemInfo{{Code: "source-degraded", Subject: staleName}}},
		},
		Objects: []model.ObjectInfo{
			{Name: fineName, Kind: kindFlags, State: "ok", ChecksumHeld: sameSum, ChecksumListed: sameSum},
			{Name: staleName, Kind: kindFlags, State: "stale", Reason: "timeout", ChecksumHeld: "held",
				ChecksumListed: "listed", Flags: 3},
		},
		Withdrawn:      []model.WithdrawnInfo{{Name: "w", Generation: 4}},
		PendingRestart: []string{"exporters"},
		Generation:     4, Snapshot: 5,
	}

	assert.Equal(t, want, configInfo(&status, now))
}

// TestConfigInfo_NoRuntime pins /info before any runtime: empty lists, not null.
func TestConfigInfo_NoRuntime(t *testing.T) {
	t.Parallel()

	got := configInfo(&relaycore.Status{}, clock.Now())

	assert.Equal(t, map[string]uint64{"applied": 0, "rejected": 0, "ignored": 0}, got.Reloads)
	assert.Equal(t, []model.FlagSetInfo{}, got.FlagSets)
	assert.Equal(t, []model.ObjectInfo{}, got.Objects)
	assert.Equal(t, []string{}, got.PendingRestart)
	assert.Equal(t, model.StalenessInfo{}, got.Staleness)
}
