package corehttp_test

import (
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
)

// TestDocs_ListsMatchTheCode keeps the page of the probes and the code from drifting: every metric, reason and state of
// the code is on the page, and the page names no config metric the code does not export.
func TestDocs_ListsMatchTheCode(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../docs/relay-proxy/probes.md")
	require.NoError(t, err)

	page := string(raw)

	t.Run("metrics", func(t *testing.T) {
		t.Parallel()

		documented := regexp.MustCompile("`(semaphore_(?:config|resource)_[a-z_]+)`").FindAllStringSubmatch(page, -1)

		var got []string

		for _, m := range documented {
			if !slices.Contains(got, m[1]) {
				got = append(got, m[1])
			}
		}

		want := corehttp.MetricNames()

		slices.Sort(got)
		slices.Sort(want)
		assert.Equal(t, want, got)
	})

	t.Run("reasons and states", func(t *testing.T) {
		t.Parallel()

		for _, word := range append(corehttp.Reasons(), corehttp.ReadyStates()...) {
			assert.Contains(t, page, "`"+word+"`", word)
		}
	})
}
