package segments_test

import (
	"go/ast"
	"go/parser"
	"go/token"
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

// claim names a test that covers a row: a package directory (relative to the module root) and a top-level Test
// function.
type claim struct{ dir, name string }

const thisDir = "internal/loadtest/segments"

func here(name string) claim { return claim{dir: thisDir, name: name} }

// outageRows maps each row to the tests that cover it: a chaos test of this package, or the test that already covers
// it elsewhere. TestOutageTable_ClaimedTestsExist fails when a claimed test is renamed or deleted.
func outageRows() map[string][]claim {
	return map[string][]claim{
		rowHealthy:      {here(testKill)},
		rowGoneUncached: {here(testKill), here(testPause), here(testLatency)},
		rowSnapNever:    {here("TestChaos_StoreDownAtStart")},
		rowSnapGone:     {here(testKill)},
		rowSnapBack:     {here(testKill)},
		rowSnapBroken:   {here("TestChaos_SnapshotSetDeletedOrBroken")},
		rowSnapHang:     {here(testPause)},
		rowSnapDeleted:  {here("TestChaos_SnapshotSetDeletedOrBroken")},
		rowGoneCached:   {here(testKill)},
		rowBreakerOpen:  {here(testKill)},
		rowStoreBack:    {here(testKill)},
		rowDefRejected: {{
			dir:  "cmd/relayproxy/corehttp",
			name: "TestE2E_DynamicDefinitionsAreCheckedAgainstTheConnections",
		}},
	}
}

// moduleRoot finds the directory of go.mod above the working directory of the test.
func moduleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)

	for {
		_, statErr := os.Stat(filepath.Join(dir, "go.mod"))
		if statErr == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "no go.mod above the test directory")

		dir = parent
	}
}

// testFuncs returns the names of the top-level Test functions of the test files of a directory.
func testFuncs(t *testing.T, dir string) map[string]bool {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	require.NoError(t, err)
	require.NotEmpty(t, files, dir)

	names := map[string]bool{}
	fset := token.NewFileSet()

	for _, file := range files {
		parsed, parseErr := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		require.NoError(t, parseErr, file)

		for _, decl := range parsed.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				names[fn.Name.Name] = true
			}
		}
	}

	return names
}

func TestOutageTable_ClaimedTestsExist(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	cache := map[string]map[string]bool{}

	for row, claims := range outageRows() {
		require.NotEmpty(t, claims, row)

		for _, claimed := range claims {
			if cache[claimed.dir] == nil {
				cache[claimed.dir] = testFuncs(t, filepath.Join(root, filepath.FromSlash(claimed.dir)))
			}

			assert.True(t, cache[claimed.dir][claimed.name],
				"the row %q claims %s in %s, which is not a top-level Test function", row, claimed.name, claimed.dir)
		}
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
