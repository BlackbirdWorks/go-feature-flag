package docstest_test

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

const budgetPage = docsRoot + "/concepts/request-budget.md"

// table returns the data rows of the table that follows the marker comment `<!-- name -->` on a page.
func table(t *testing.T, marker string) [][]string {
	t.Helper()

	raw, err := os.ReadFile(budgetPage)
	require.NoError(t, err)

	_, after, found := strings.Cut(string(raw), "<!-- "+marker+" -->\n")
	require.True(t, found, "marker %q", marker)

	var rows [][]string

	for i, line := range strings.Split(after, "\n") {
		if !strings.HasPrefix(line, "|") {
			break
		}

		if i < 2 { // header and rule
			continue
		}

		cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|"), "|")
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}

		rows = append(rows, cells)
	}

	require.NotEmpty(t, rows)

	return rows
}

func number(t *testing.T, text string) float64 {
	t.Helper()

	value, err := strconv.ParseFloat(strings.Trim(text, "`"), 64)
	require.NoError(t, err, text)

	return value
}

// fmtNumber writes a number as the page does: whole numbers without a fraction.
func fmtNumber(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

func usd(value float64) string { return fmt.Sprintf("%.2f", math.Round(value*100)/100) }

// Every number of the request budget page is computed from the formula, with the default poll interval of the code.
func TestRequestBudgetTablesFollowTheFormula(t *testing.T) {
	t.Parallel()

	inputs := table(t, "budget: inputs")
	require.Len(t, inputs, 3)

	interval := number(t, inputs[0][1])
	month := number(t, inputs[1][1])
	price := number(t, inputs[2][1]) / 1000

	assert.InDelta(
		t,
		manifestreader.DefaultPollInterval.Seconds(),
		interval,
		0,
		"the page states the default of the reader",
	)
	assert.InDelta(
		t,
		bootstrap.DefaultPollInterval.Seconds(),
		interval,
		0,
		"the page states the default of the bootstrap file",
	)

	perMinute := func(interval float64) float64 { return 60 / interval }

	t.Run("fleet", func(t *testing.T) {
		t.Parallel()

		for _, row := range table(t, "budget: fleet") {
			replicas := number(t, row[0])
			minute := replicas * perMinute(interval)

			assert.Equal(t, []string{
				fmtNumber(replicas), fmtNumber(minute), fmtNumber(minute * 1440), fmtNumber(minute * month),
				usd(minute * month * price),
			}, row)
		}
	})

	t.Run("interval", func(t *testing.T) {
		t.Parallel()

		for _, row := range table(t, "budget: interval") {
			seconds := number(t, row[0])

			assert.Equal(t, []string{
				fmtNumber(seconds), fmtNumber(perMinute(seconds)), fmtNumber(perMinute(seconds) * month),
			}, row)
		}
	})

	t.Run("change", func(t *testing.T) {
		t.Parallel()

		for _, row := range table(t, "budget: change") {
			replicas, changed := number(t, row[0]), number(t, row[1])

			assert.Equal(t, []string{fmtNumber(replicas), fmtNumber(changed), fmtNumber(replicas * changed)}, row)
		}
	})

	t.Run("latency", func(t *testing.T) {
		t.Parallel()

		jitter := interval * manifestreader.DefaultJitterFraction
		spread := manifestreader.DefaultFetchSpread.Seconds()

		want := [][]string{
			{"Poll interval", fmtNumber(interval)},
			{"Jitter (10 %)", fmtNumber(jitter)},
			{"Fetch delay (at most)", fmtNumber(spread)},
			{"Total before the download", fmtNumber(interval + jitter + spread)},
		}

		assert.Equal(t, want, table(t, "budget: latency"))
	})

	t.Run("start", func(t *testing.T) {
		t.Parallel()

		const (
			manifests = 1
			objects   = 5
		)

		want := [][]string{
			{"No saved state", "`C + N`", fmtNumber(manifests + objects)},
			{"Current state directory", "`C`", fmtNumber(manifests)},
		}

		assert.Equal(t, want, table(t, "budget: start"))
	})
}

// The page names the default of the code in words.
func TestRequestBudgetStatesTheDefaultInterval(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(budgetPage)
	require.NoError(t, err)

	assert.Contains(
		t,
		string(raw),
		fmt.Sprintf("default poll interval is %d s", int(manifestreader.DefaultPollInterval.Seconds())),
	)
}
