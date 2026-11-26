package metric_test

import (
	"strings"
	"testing"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

const (
	gaugeKind   = "gauge"
	counterKind = "counter"

	generationName = "semaphore_manifest_generation"
	lastSyncName   = "semaphore_manifest_last_sync_timestamp_seconds"
	pollsName      = "semaphore_manifest_polls_total"
	objectsName    = "semaphore_manifest_objects"
	failuresName   = "semaphore_manifest_object_failures"
	savesName      = "semaphore_manifest_state_saves_total"
	restoreName    = "semaphore_manifest_state_restore_total"

	generationHelp = "Generation of the manifest the reader holds (the applied one)."
	lastSyncHelp   = "Unix time of the last poll that confirmed the held manifest (a 200 that was accepted or a 304); " +
		"0 when there was none."
	pollsHelp    = "Manifest polls by result (not_modified, updated, not_found, too_large, error)."
	objectsHelp  = "Objects of the held manifest by state (ok, stale, missing)."
	failuresHelp = "Objects of the held manifest that are not ok (stale or missing); " +
		"names and reasons are in the status."
	savesHelp   = "Writes of the state directory by result (ok, error); a failed write never stops polling."
	restoreHelp = "Restore of the state directory at start by result (restored, partial, empty, failed)."
)

// family is the text of one metric family as the exposition format prints it, for the series of the control
// connection: each line is the labels and the value.
func family(name, help, kind string, series ...string) string {
	lines := make([]string, len(series))
	for i, one := range series {
		lines[i] = name + one
	}

	return "# HELP " + name + " " + help + "\n# TYPE " + name + " " + kind + "\n" + strings.Join(lines, "\n") + "\n"
}

func TestManifestReader_Collect(t *testing.T) {
	t.Parallel()

	synced := time.Unix(1_700_000_000, 0)

	zeroPolls := family(pollsName, pollsHelp, counterKind,
		`{connection="control",result="error"} 0`, `{connection="control",result="not_found"} 0`,
		`{connection="control",result="not_modified"} 0`, `{connection="control",result="too_large"} 0`,
		`{connection="control",result="updated"} 0`)

	tests := []struct {
		name   string
		want   string
		names  []string
		status manifestreader.Status
	}{
		{
			name:   "before anything happened: no generation, zero counters",
			status: manifestreader.Status{},
			names:  []string{generationName, lastSyncName, pollsName, savesName},
			want:   family(lastSyncName, lastSyncHelp, gaugeKind, `{connection="control"} 0`) + zeroPolls,
		},
		{
			name: "generation, last sync and the polls by result",
			status: manifestreader.Status{
				LastSync: synced,
				Manifest: manifestreader.ManifestStatus{
					Outcome: manifestreader.OutcomeAccepted, Channel: "stable", Generation: 7,
				},
				PollResults: []manifestreader.PollCount{
					{Result: manifestreader.PollError, Count: 2},
					{Result: manifestreader.PollNotModified, Count: 40},
					{Result: manifestreader.PollUpdated, Count: 3},
				},
			},
			names: []string{generationName, lastSyncName, pollsName},
			want: family(generationName, generationHelp, gaugeKind, `{channel="stable",connection="control"} 7`) +
				family(lastSyncName, lastSyncHelp, gaugeKind, `{connection="control"} 1.7e+09`) +
				family(pollsName, pollsHelp, counterKind,
					`{connection="control",result="error"} 2`, `{connection="control",result="not_found"} 0`,
					`{connection="control",result="not_modified"} 40`, `{connection="control",result="too_large"} 0`,
					`{connection="control",result="updated"} 3`),
		},
		{
			name: "objects by state and the failure count, never a name",
			status: manifestreader.Status{
				Counts: manifestreader.Counts{OK: 5, Stale: 1, Missing: 2},
				Failures: []manifestreader.ObjectFailure{
					{Name: "a", State: manifestreader.StateStale}, {Name: "b", State: manifestreader.StateMissing},
					{Name: "c", State: manifestreader.StateMissing},
				},
			},
			names: []string{objectsName, failuresName},
			want: family(failuresName, failuresHelp, gaugeKind, `{connection="control"} 3`) +
				family(objectsName, objectsHelp, gaugeKind,
					`{connection="control",state="missing"} 2`, `{connection="control",state="ok"} 5`,
					`{connection="control",state="stale"} 1`),
		},
		{
			name: "backoff state",
			status: manifestreader.Status{
				Backoff: manifestreader.BackoffStatus{Failures: 3, Wait: 150 * time.Second, ObjectsWaiting: 2},
			},
			names: []string{
				"semaphore_manifest_backoff_failures", "semaphore_manifest_backoff_wait_seconds",
				"semaphore_manifest_backoff_objects_waiting",
			},
			want: family("semaphore_manifest_backoff_failures", "Consecutive failed manifest polls; 0 after a success.",
				gaugeKind, `{connection="control"} 3`) +
				family("semaphore_manifest_backoff_objects_waiting",
					"Failed objects that wait for their next retry (retry backoff).", gaugeKind,
					`{connection="control"} 2`) +
				family("semaphore_manifest_backoff_wait_seconds", "Wait planned before the next manifest poll.",
					gaugeKind, `{connection="control"} 150`),
		},
		{
			name: "state directory: saves and the restore result",
			status: manifestreader.Status{State: manifestreader.StateStatus{
				Enabled: true, Saves: 5, SaveErrors: 2, Restore: manifestreader.RestorePartial,
			}},
			names: []string{savesName, restoreName},
			want: family(restoreName, restoreHelp, counterKind,
				`{connection="control",result="empty"} 0`, `{connection="control",result="failed"} 0`,
				`{connection="control",result="partial"} 1`, `{connection="control",result="restored"} 0`) +
				family(savesName, savesHelp, counterKind,
					`{connection="control",result="error"} 2`, `{connection="control",result="ok"} 3`),
		},
		{
			name:   "no state directory: no state series",
			status: manifestreader.Status{},
			names:  []string{savesName, restoreName},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reg := prom.NewRegistry()
			_, err := metric.NewManifestReader(reg, connControl, func() manifestreader.Status { return tt.status })
			require.NoError(t, err)

			assert.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(tt.want), tt.names...))
		})
	}
}

func TestManifestReader_SeriesAreClosedAndFollowTheStatus(t *testing.T) {
	t.Parallel()

	status := manifestreader.Status{}
	reg := prom.NewRegistry()
	_, err := metric.NewManifestReader(reg, connControl, func() manifestreader.Status { return status })
	require.NoError(t, err)

	count := func() int {
		n, countErr := testutil.GatherAndCount(reg)
		require.NoError(t, countErr)

		return n
	}

	empty := count()

	// a manifest with thousands of failing objects adds no series: only the values change
	failures := make([]manifestreader.ObjectFailure, 5000)
	for i := range failures {
		failures[i] = manifestreader.ObjectFailure{
			Name: "object-" + strings.Repeat("x", i%7), State: manifestreader.StateMissing,
		}
	}

	status = manifestreader.Status{
		Failures: failures, Counts: manifestreader.Counts{Missing: len(failures)},
		Manifest: manifestreader.ManifestStatus{Channel: "stable", Generation: 1},
	}

	assert.Equal(
		t,
		empty+1,
		count(),
		"the generation series appears with the first accepted manifest; nothing else grows",
	)
}

func TestNewManifestReader_RegisteredTwiceFails(t *testing.T) {
	t.Parallel()

	reg := prom.NewRegistry()
	status := func() manifestreader.Status { return manifestreader.Status{} }

	_, err := metric.NewManifestReader(reg, connControl, status)
	require.NoError(t, err)

	_, err = metric.NewManifestReader(reg, connControl, status)
	assert.Error(t, err)
}

func TestNewMetrics_DoesNotExposeManifestReader(t *testing.T) {
	t.Parallel()

	metrics, err := metric.NewMetrics()
	require.NoError(t, err)

	families, err := metrics.Registry.Gather()
	require.NoError(t, err)

	for _, one := range families {
		assert.False(t, strings.HasPrefix(one.GetName(), "semaphore_manifest_"), one.GetName())
	}
}
