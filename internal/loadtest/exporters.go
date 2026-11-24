package loadtest

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

// ExporterMode selects the behaviour of the exporter used by the harness and the benchmarks.
type ExporterMode string

const (
	// ExporterDiscard accepts and drops every batch immediately.
	ExporterDiscard ExporterMode = "discard"
	// ExporterSlow sleeps for SlowDelay on every batch before accepting it.
	ExporterSlow ExporterMode = "slow"
	// ExporterFailing returns an error on every batch.
	ExporterFailing ExporterMode = "failing"
)

// ParseExporterMode validates an exporter mode name.
func ParseExporterMode(s string) (ExporterMode, error) {
	switch m := ExporterMode(s); m {
	case ExporterDiscard, ExporterSlow, ExporterFailing:
		return m, nil
	default:
		return "", fmt.Errorf("unknown exporter %q (want discard | slow | failing)", s)
	}
}

// SlowDelay is the time the "slow" exporter blocks on each Export call.
const SlowDelay = 2 * time.Second

// Exporter is a bulk exporter with a configurable behaviour that counts what it received.
type Exporter struct {
	Mode     ExporterMode
	Delay    time.Duration
	Batches  atomic.Int64
	Events   atomic.Int64
	Failures atomic.Int64
}

var _ exporter.Exporter = (*Exporter)(nil)

// NewExporter creates an Exporter for the mode.
func NewExporter(mode ExporterMode) *Exporter {
	return &Exporter{Mode: mode, Delay: SlowDelay}
}

// IsBulk implements exporter.CommonExporter.
func (e *Exporter) IsBulk() bool { return true }

// Export implements exporter.Exporter.
func (e *Exporter) Export(ctx context.Context, _ *fflog.FFLogger, events []exporter.ExportableEvent) error {
	switch e.Mode {
	case ExporterFailing:
		e.Failures.Add(1)

		return errors.New("loadtest: exporter is failing")
	case ExporterSlow:
		select {
		case <-time.After(e.Delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	e.Batches.Add(1)
	e.Events.Add(int64(len(events)))

	return nil
}
