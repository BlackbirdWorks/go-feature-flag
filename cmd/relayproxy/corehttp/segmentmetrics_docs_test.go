package corehttp_test

import (
	"os"
	"regexp"
	"slices"
	"testing"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/segmentobs"
)

type fixedStatus struct{ status *relaycore.Status }

func (f fixedStatus) Status() relaycore.Status { return *f.status }

const connAudiencesName = "audiences"

var segmentFamily = regexp.MustCompile("`(semaphore_segment_[a-z_]+)`")

// Every segment series of the code is in the table of the probes page with its labels, and the page lists none that the
// code does not export.
func TestDocs_SegmentMetricsMatchTheCode(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../docs/relay-proxy/probes.md")
	require.NoError(t, err)

	page := string(raw)

	reg := prom.NewPedanticRegistry()
	require.NoError(t, reg.Register(corehttp.NewSegmentCollector(fixedStatus{status: &relaycore.Status{}})))
	require.NoError(t, reg.Register(segmentobs.New(segmentobs.Options{PerSegment: true})))

	var want []string

	descs := make(chan *prom.Desc, 64)

	go func() {
		corehttp.NewSegmentCollector(fixedStatus{status: &relaycore.Status{}}).Describe(descs)
		segmentobs.New(segmentobs.Options{PerSegment: true}).Describe(descs)
		close(descs)
	}()

	for d := range descs {
		match := regexp.MustCompile(`fqName: "([a-z_]+)"`).FindStringSubmatch(d.String())
		require.Len(t, match, 2)
		want = append(want, match[1])
	}

	var got []string

	for _, m := range segmentFamily.FindAllStringSubmatch(page, -1) {
		if !slices.Contains(got, m[1]) {
			got = append(got, m[1])
		}
	}

	slices.Sort(want)
	slices.Sort(got)
	assert.Equal(t, want, got)

	for _, label := range []string{"`connection`", "`result`", "`reason`", "`state`", "`segment`"} {
		assert.Contains(t, page, label)
	}
}

// The breaker gauge counts the segments in each unhealthy state of each connection.
func TestSegmentCollector_BreakerState(t *testing.T) {
	t.Parallel()

	src := fixedStatus{status: &relaycore.Status{Segments: relaycore.SegmentsStatus{
		Bindings: flagruntime.SegmentBindingsStatus{Breakers: []flagruntime.ConnectionBreakers{
			{Connection: "audiences", Open: 2, HalfOpen: 1},
			{Connection: "staff"},
		}},
	}}}

	reg := prom.NewPedanticRegistry()
	require.NoError(t, reg.Register(corehttp.NewSegmentCollector(src)))

	families, err := reg.Gather()
	require.NoError(t, err)

	got := map[string]float64{}

	for _, fam := range families {
		if fam.GetName() != "semaphore_segment_breaker_state" {
			continue
		}

		for _, m := range fam.GetMetric() {
			got[m.GetLabel()[0].GetValue()+"/"+m.GetLabel()[1].GetValue()] = m.GetGauge().GetValue()
		}
	}

	assert.Equal(t, map[string]float64{
		"audiences/open": 2, "audiences/half_open": 1, "staff/open": 0, "staff/half_open": 0,
	}, got)
}

// The count of sets installed without a stable version is exported for each connection, and /info says the same.
func TestSegmentCollectorExportsTheUnsettledInstalls(t *testing.T) {
	t.Parallel()

	status := &relaycore.Status{}
	status.Segments.Bindings.Connections = []flagruntime.ConnectionSnapshots{
		{Connection: connAudiencesName, Unsettled: 3}, {Connection: connOther, Unsettled: 1},
	}

	reg := prom.NewPedanticRegistry()
	require.NoError(t, reg.Register(corehttp.NewSegmentCollector(fixedStatus{status: status})))

	families, err := reg.Gather()
	require.NoError(t, err)

	got := map[string]float64{}

	for _, family := range families {
		if family.GetName() != "semaphore_segment_snapshot_unsettled_total" {
			continue
		}

		for _, metric := range family.GetMetric() {
			got[metric.GetLabel()[0].GetValue()] = metric.GetCounter().GetValue()
		}
	}

	assert.Equal(t, map[string]float64{connAudiencesName: 3, connOther: 1}, got)
}
