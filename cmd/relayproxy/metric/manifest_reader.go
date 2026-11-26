package metric

import (
	"fmt"

	prom "github.com/prometheus/client_golang/prometheus"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// ManifestReader exposes the [manifestreader.Status] of one manifest reader as Prometheus series, read at scrape
// time: the reader does not import Prometheus and nothing is pushed on the poll path. Every series carries the
// name of the connection (fixed by the bootstrap file); the other labels are closed sets (a poll result, an
// object state, a save result, a restore result) and the channel of the manifest. There is no per-object or
// per-flag label, so the number of series does not grow with the manifest.
//
// Like [S3Requests] it is not part of [Metrics] and not registered by [NewMetrics]: the relay registers it when
// it runs the reader (config v2), so the output of the running relay does not change before.
type ManifestReader struct {
	status func() manifestreader.Status
	desc   manifestDescs
	conn   string
}

type manifestDescs struct {
	generation   *prom.Desc
	lastSync     *prom.Desc
	polls        *prom.Desc
	objects      *prom.Desc
	failures     *prom.Desc
	saves        *prom.Desc
	restore      *prom.Desc
	backoffFails *prom.Desc
	backoffWait  *prom.Desc
	backoffObjs  *prom.Desc
}

var _ prom.Collector = (*ManifestReader)(nil)

// pollResults is the closed set of the poll results the reader reports (without "none").
func pollResults() []string {
	return []string{
		manifestreader.PollNotModified, manifestreader.PollUpdated, manifestreader.PollNotFound,
		manifestreader.PollTooLarge, manifestreader.PollError,
	}
}

// restoreResults is the closed set of the restore results.
func restoreResults() []string {
	return []string{
		manifestreader.RestoreRestored, manifestreader.RestorePartial, manifestreader.RestoreEmpty,
		manifestreader.RestoreFailed,
	}
}

const nanosPerSecond = 1e9

// NewManifestReader registers the collector of the reader of connection on reg. status is called at every scrape
// and must not block ([manifestreader.Reader.Status] does not).
func NewManifestReader(
	reg prom.Registerer, connectionName string, status func() manifestreader.Status,
) (*ManifestReader, error) {
	name := func(suffix string) string { return prom.BuildFQName(SubSystem, "manifest", suffix) }
	labels := []string{"connection"}
	desc := func(suffix, help string, extra ...string) *prom.Desc {
		return prom.NewDesc(name(suffix), help, append(append([]string(nil), labels...), extra...), nil)
	}

	collector := &ManifestReader{
		status: status, conn: connectionName,
		desc: manifestDescs{
			generation: desc("generation", "Generation of the manifest the reader holds (the applied one).", "channel"),
			lastSync: desc("last_sync_timestamp_seconds",
				"Unix time of the last poll that confirmed the held manifest (a 200 that was accepted or a 304); "+
					"0 when there was none."),
			polls: desc("polls_total",
				"Manifest polls by result (not_modified, updated, not_found, too_large, error).", "result"),
			objects: desc("objects", "Objects of the held manifest by state (ok, stale, missing).", "state"),
			failures: desc(
				"object_failures",
				"Objects of the held manifest that are not ok (stale or missing); names and reasons are in the status.",
			),
			saves: desc("state_saves_total",
				"Writes of the state directory by result (ok, error); a failed write never stops polling.", "result"),
			restore: desc("state_restore_total",
				"Restore of the state directory at start by result (restored, partial, empty, failed).", "result"),
			backoffFails: desc("backoff_failures", "Consecutive failed manifest polls; 0 after a success."),
			backoffWait:  desc("backoff_wait_seconds", "Wait planned before the next manifest poll."),
			backoffObjs: desc("backoff_objects_waiting",
				"Failed objects that wait for their next retry (retry backoff)."),
		},
	}

	err := reg.Register(collector)
	if err != nil {
		return nil, fmt.Errorf("register manifest reader metrics: %w", err)
	}

	return collector, nil
}

// Describe implements [prom.Collector].
func (m *ManifestReader) Describe(descs chan<- *prom.Desc) {
	for _, desc := range []*prom.Desc{
		m.desc.generation, m.desc.lastSync, m.desc.polls, m.desc.objects, m.desc.failures, m.desc.saves,
		m.desc.restore, m.desc.backoffFails, m.desc.backoffWait, m.desc.backoffObjs,
	} {
		descs <- desc
	}
}

// Collect implements [prom.Collector].
func (m *ManifestReader) Collect(metrics chan<- prom.Metric) {
	status := m.status()

	gauge := func(desc *prom.Desc, value float64, labels ...string) {
		metrics <- prom.MustNewConstMetric(desc, prom.GaugeValue, value, append([]string{m.conn}, labels...)...)
	}
	counter := func(desc *prom.Desc, value float64, labels ...string) {
		metrics <- prom.MustNewConstMetric(desc, prom.CounterValue, value, append([]string{m.conn}, labels...)...)
	}

	if status.Manifest.Channel != "" {
		gauge(m.desc.generation, float64(status.Manifest.Generation), status.Manifest.Channel)
	}

	lastSync := 0.0
	if !status.LastSync.IsZero() {
		lastSync = float64(status.LastSync.UnixNano()) / nanosPerSecond
	}

	gauge(m.desc.lastSync, lastSync)

	counts := map[string]uint64{}
	for _, result := range status.PollResults {
		counts[result.Result] = result.Count
	}

	for _, result := range pollResults() {
		counter(m.desc.polls, float64(counts[result]), result)
	}

	gauge(m.desc.objects, float64(status.Counts.OK), string(manifestreader.StateOK))
	gauge(m.desc.objects, float64(status.Counts.Stale), string(manifestreader.StateStale))
	gauge(m.desc.objects, float64(status.Counts.Missing), string(manifestreader.StateMissing))
	gauge(m.desc.failures, float64(len(status.Failures)))

	m.collectState(&status, counter)

	gauge(m.desc.backoffFails, float64(status.Backoff.Failures))
	gauge(m.desc.backoffWait, status.Backoff.Wait.Seconds())
	gauge(m.desc.backoffObjs, float64(status.Backoff.ObjectsWaiting))
}

// collectState emits the series of the state directory, only when one is installed.
func (m *ManifestReader) collectState(status *manifestreader.Status, counter func(*prom.Desc, float64, ...string)) {
	state := status.State
	if !state.Enabled {
		return
	}

	counter(m.desc.saves, float64(state.Saves-state.SaveErrors), "ok")
	counter(m.desc.saves, float64(state.SaveErrors), "error")

	for _, result := range restoreResults() {
		value := 0.0
		if state.Restore == result {
			value = 1
		}

		counter(m.desc.restore, value, result)
	}
}
