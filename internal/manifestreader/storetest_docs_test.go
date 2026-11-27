package manifestreader_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader/storetest"
)

// TestDocsListEveryCaseOfTheSuite: the table of cases in the docs page is the list of the suite, with the
// capability each case needs. A case added to the suite fails this test until the page lists it.
func TestDocsListEveryCaseOfTheSuite(t *testing.T) {
	t.Parallel()

	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "concepts", "control-plane-file-store.md"))
	require.NoError(t, err)

	row := regexp.MustCompile("^\\| `([a-z0-9-]+)` \\| ([^|]+) \\|")

	var names []string

	needs := map[string]string{}

	for line := range strings.SplitSeq(string(page), "\n") {
		match := row.FindStringSubmatch(line)
		if match == nil {
			continue
		}

		names = append(names, match[1])
		needs[match[1]] = strings.Trim(strings.TrimSpace(match[2]), "`")
	}

	slices.Sort(names)
	assert.Equal(t, storetest.CaseNames(), names, "the cases of the docs table and of the suite")

	for _, name := range storetest.CaseNames() {
		capabilities, ok := storetest.CaseNeeds(name)
		require.True(t, ok, name)

		want := "-"
		if len(capabilities) > 0 {
			want = string(capabilities[0])
		}

		assert.Equal(t, want, needs[name], "the capability the docs name for %s", name)
	}
}
