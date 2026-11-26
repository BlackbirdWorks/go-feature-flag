package manifestreader_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

// canarySum is a checksum, which the status shows (it is not a secret).
const canarySum = "sha256:00112233"

// The JSON of the status is part of what the later endpoints show: the field names are camelCase, every field is
// there, and the status holds no key or credential. The golden file pins the names and the shape; a renamed or
// untagged field changes it.
func TestStatus_JSONGolden(t *testing.T) {
	t.Parallel()

	instant := clocktest.Instant()
	status := manifestreader.Status{
		LastSync: instant,
		Poll:     manifestreader.PollStatus{At: instant, Result: manifestreader.PollUpdated, Reason: ""},
		Failures: []manifestreader.ObjectFailure{{
			Name: "flags-a", State: manifestreader.StateStale, Reason: manifestreader.ReasonChecksumMismatch,
			Checksum: canarySum, Retryable: true,
		}},
		HookPanics:  []manifestreader.HookPanic{{Name: "custom", Count: 2}},
		PollResults: []manifestreader.PollCount{{Result: manifestreader.PollUpdated, Count: 3}},
		Manifest: manifestreader.ManifestStatus{
			Outcome: manifestreader.OutcomeAccepted, Channel: "stable", Generation: 7, Objects: 4, UnknownFields: 1,
		},
		State: manifestreader.StateStatus{
			LastSave:         instant,
			Restore:          manifestreader.RestorePartial,
			LastSaveError:    "write",
			ParentSync:       manifestreader.ParentSyncPending,
			DisabledReason:   "",
			Saves:            5,
			SaveErrors:       1,
			PartialSaves:     2,
			RestoredObjects:  3,
			DiscardedObjects: 1,
			Enabled:          true,
			LastSavePartial:  true,
			Pending:          true,
		},
		Backoff: manifestreader.BackoffStatus{
			Wait:           90 * time.Second,
			Failures:       2,
			ObjectsWaiting: 1,
			Throttled:      true,
		},
		Counts:      manifestreader.Counts{OK: 2, Stale: 1, Missing: 1},
		Cache:       manifestreader.CacheStatus{Objects: 3, Bytes: 1234},
		Polls:       9,
		NotModified: 6,
	}

	got, err := json.Marshal(status)
	require.NoError(t, err)

	want, err := os.ReadFile("testdata/status.golden.json")
	require.NoError(t, err)

	assert.JSONEq(t, string(want), string(got))
}
