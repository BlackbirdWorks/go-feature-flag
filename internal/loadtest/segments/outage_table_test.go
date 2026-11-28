package segments_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rows of the outage table of docs/configure-flags/segments-dynamic.md ("What happens when the store is down"),
// each by the start of its first column. The chaos tests name the row that every assertion checks, and
// TestOutageTable_EveryRowIsClaimed fails when the table gains, loses or renames a row, so the page and the
// behaviour cannot drift apart without a test noticing.
const (
	rowHealthy       = "Healthy store"
	rowGoneUncached  = "Never connected, or gone, and the answer is not cached"
	rowSnapNever     = "`snapshot`: never loaded"
	rowSnapGone      = "`snapshot`: loaded, then the store is gone"
	rowSnapBack      = "`snapshot`: the store is back"
	rowSnapBroken    = "`snapshot`: one set the store cannot give"
	rowSnapHang      = "`snapshot`: a store call hangs"
	rowSnapDeleted   = "`snapshot`: the set was deleted"
	rowGoneCached    = "Gone, and the answer is cached"
	rowBreakerOpen   = "Breaker open"
	rowStoreBack     = "Store is back"
	rowDefRejected   = "Definition rejected"
	outageTableTitle = "## What happens when the store is down"

	testKill    = "TestChaos_KillStore"
	testPause   = "TestChaos_PauseStore"
	testLatency = "TestChaos_AddedLatencyAndCut"
)

// outageRows maps each row to where it is tested: a chaos test of this package, or the test that already covers it.
func outageRows() map[string]string {
	return map[string]string{
		rowHealthy:      testKill,
		rowGoneUncached: testKill + ", " + testPause + ", " + testLatency,
		rowSnapNever:    "TestChaos_StoreDownAtStart",
		rowSnapGone:     testKill,
		rowSnapBack:     testKill,
		rowSnapBroken:   "TestChaos_SnapshotSetDeletedOrBroken",
		rowSnapHang:     "TestChaos_PauseStore",
		rowSnapDeleted:  "TestChaos_SnapshotSetDeletedOrBroken",
		rowGoneCached:   testKill,
		rowBreakerOpen:  testKill,
		rowStoreBack:    testKill,
		rowDefRejected:  "cmd/relayproxy/corehttp TestE2E_DynamicSegmentDefinitionRejected (not repeated here)",
	}
}

// docRows reads the first column of the outage table.
func docRows(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "configure-flags", "segments-dynamic.md"))
	require.NoError(t, err)

	_, after, found := strings.Cut(string(raw), outageTableTitle)
	require.True(t, found, "the page has the section %q", outageTableTitle)

	var rows []string

	for line := range strings.SplitSeq(after, "\n") {
		if !strings.HasPrefix(line, "|") {
			if len(rows) > 0 {
				break
			}

			continue
		}

		cells := strings.Split(line, "|")
		if first := strings.TrimSpace(cells[1]); first != "Situation" && !strings.HasPrefix(first, "---") {
			rows = append(rows, first)
		}
	}

	return rows
}

func TestOutageTable_EveryRowIsClaimed(t *testing.T) {
	t.Parallel()

	claims := outageRows()
	rows := docRows(t)

	assert.Len(t, rows, len(claims), "the table has one row for each claim: %v", rows)

	for key := range claims {
		matches := 0

		for _, row := range rows {
			if strings.HasPrefix(row, key) {
				matches++
			}
		}

		assert.Equal(t, 1, matches, "the table has exactly one row that starts with %q", key)
	}
}
