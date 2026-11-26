package events

import (
	"context"
	"log/slog"
	"runtime"
	"slices"
	"sync"
)

// ReloadSet says what a reload did with the exporters (or the notifiers), by name.
type ReloadSet struct {
	// Kept are the entries whose identity did not change: their manager (and its queued events) stayed.
	Kept []string
	// Added are the new entries.
	Added []string
	// Replaced are the entries whose identity changed: a new one runs, the old one was drained.
	Replaced []string
	// Removed are the entries that left: they were drained.
	Removed []string
	// Cut are the drained entries whose drain passed the deadline.
	Cut []string
}

// DrainResult is the outcome of the drain of one exporter.
type DrainResult struct {
	Name string
	Kind string
	// Undelivered is the number of events that were not delivered when the exporter closed.
	Undelivered int64
	// Cut says that the drain passed the deadline and was abandoned.
	Cut bool
}

// ShutdownReport says what a shutdown drained and dropped.
type ShutdownReport struct {
	// Drained has one entry per exporter that was closed.
	Drained []DrainResult
	// Dropped counts the events dropped over the life of the dispatcher, by reason.
	Dropped Drops
	// Undelivered is the sum of the undelivered events of the exporters.
	Undelivered int64
}

// Reload builds the exporters of the specs and applies them in place, see [Dispatcher.Apply]. A build error
// changes nothing.
func (d *Dispatcher) Reload(ctx context.Context, specs Specs) (ReloadSet, error) {
	defs, err := d.cfg.Builder.Exporters(ctx, specs)
	if err != nil {
		return ReloadSet{}, err
	}

	return d.Apply(ctx, defs)
}

// Apply replaces the set of exporters by defs, in place. An exporter whose name and identity did not change
// keeps its manager and the events it holds. A new or changed one gets a new manager; the old manager of a
// changed or removed one is no longer given events and is drained (stopped, which flushes it) with a deadline of
// the drain timeout, in parallel and off the hot path. Events handed over before the swap went to the old
// managers and are flushed by their drain; events after it go to the new ones; a batch goes to one set only.
func (d *Dispatcher) Apply(ctx context.Context, defs []ExporterDef) (ReloadSet, error) {
	d.lifecycle.Lock()
	defer d.lifecycle.Unlock()

	if d.closed.Load() {
		return ReloadSet{}, ErrClosed
	}

	old := d.state.Load()
	current := make(map[string]*managed, len(old.active))

	for _, m := range old.active {
		current[m.def.Name] = m
	}

	var (
		set     ReloadSet
		next    = make([]*managed, 0, len(defs))
		leaving []*managed
	)

	for _, def := range defs {
		prev := current[def.Name]
		delete(current, def.Name)

		switch {
		case prev != nil && prev.def.Identity == def.Identity:
			next = append(next, prev)
			set.Kept = append(set.Kept, def.Name)
		default:
			next = append(next, d.launch(&def))

			if prev == nil {
				set.Added = append(set.Added, def.Name)

				continue
			}

			set.Replaced = append(set.Replaced, def.Name)
			leaving = append(leaving, prev)
		}
	}

	for _, m := range old.active {
		if _, gone := current[m.def.Name]; gone {
			set.Removed = append(set.Removed, m.def.Name)
			leaving = append(leaving, m)
		}
	}

	for _, m := range leaving {
		m.state.Store(stateDraining)
	}

	d.swap(&dispatchState{active: next, retired: leaving})

	drainCtx, cancel := context.WithTimeout(ctx, d.cfg.DrainTimeout)
	defer cancel()

	for _, res := range d.drainAll(drainCtx, leaving) {
		if res.Cut {
			set.Cut = append(set.Cut, res.Name)
		}
	}

	d.swap(&dispatchState{active: next, retired: leaving})

	return set, nil
}

// launch builds and starts the manager of an exporter.
func (d *Dispatcher) launch(def *ExporterDef) *managed {
	mgr := d.cfg.NewManager(def.Name, def.Config, d.cfg.Logger.With(slog.String("exporter", def.Name)))
	mgr.Start()

	return newManaged(def, mgr)
}

// swap publishes a new set: no batch is in delivery while it is stored.
func (d *Dispatcher) swap(next *dispatchState) {
	d.mu.Lock()
	d.state.Store(next)

	// after the shutdown the count stays, so that a late Collect is counted as dropped
	if !d.closed.Load() {
		d.active.Store(int32(len(next.active))) //nolint:gosec // the number of exporters is small
	}

	d.mu.Unlock()
}

// drainAll drains the managers in parallel, each with the deadline of ctx.
func (d *Dispatcher) drainAll(ctx context.Context, list []*managed) []DrainResult {
	out := make([]DrainResult, len(list))

	var wg sync.WaitGroup

	for i, m := range list {
		wg.Go(func() { out[i] = d.drain(ctx, m) })
	}

	wg.Wait()

	return out
}

// drain stops the manager, which flushes what it holds, and waits for it until the deadline of ctx. A manager
// that is still flushing at the deadline is left to finish in its goroutine; what it did not deliver is counted
// as dropped.
func (d *Dispatcher) drain(ctx context.Context, mgd *managed) DrainResult {
	done := make(chan struct{})

	go func() {
		defer close(done)

		mgd.mgr.Stop()
	}()

	cut := false

	select {
	case <-done:
	case <-ctx.Done():
		cut = true
	}

	mgd.overflow.Store(overflowOf(mgd))

	if gone := mgd.def.stats.exported.Load() + mgd.overflow.Load(); mgd.handed.Load() > gone {
		mgd.cut.Store(mgd.handed.Load() - gone)
	}

	if cut {
		mgd.state.Store(stateCut)
		d.cfg.Logger.LogAttrs(ctx, slog.LevelWarn, "exporter drain passed the deadline",
			slog.String("exporter", mgd.def.Name), slog.Uint64("undelivered", mgd.cut.Load()))
	} else {
		mgd.state.Store(stateStopped)
	}

	return DrainResult{
		Name:        mgd.def.Name,
		Kind:        mgd.def.Kind,
		Undelivered: asInt(mgd.cut.Load()),
		Cut:         cut,
	}
}

// asInt converts an event count: counts do not reach 2^63.
func asInt(count uint64) int64 { return int64(count) } //nolint:gosec // event counts do not reach 2^63

func overflowOf(mgd *managed) uint64 {
	if counter, ok := mgd.mgr.(interface{ DroppedEvents() int64 }); ok {
		return uint64(max(counter.DroppedEvents(), 0))
	}

	return 0
}

// Done is closed when [Dispatcher.Shutdown] finished.
func (d *Dispatcher) Done() <-chan struct{} { return d.done }

// Shutdown stops taking events, delivers what the channel holds to the exporters, stops and flushes every
// exporter, and reports what was drained and dropped. The deadline of ctx bounds the whole: past it the events
// still in the channel and the events an exporter did not deliver are dropped and counted. After it, Collect
// drops and counts. A second call does nothing and returns the first report. No goroutine of the dispatcher is
// left, except one manager that was cut while it still flushed.
func (d *Dispatcher) Shutdown(ctx context.Context) (ShutdownReport, error) {
	d.lifecycle.Lock()
	defer d.lifecycle.Unlock()

	if d.isDone {
		return d.report, nil
	}

	d.isDone = true
	defer close(d.done)

	if d.unwatch != nil {
		d.unwatch()
	}

	d.closed.Store(true)
	d.waitCollectors(ctx)

	// a deadline that has passed already: the workers drop what is queued instead of delivering it
	if ctx.Err() != nil {
		d.abort(ctx)
	}

	close(d.stop)

	workersDone := make(chan struct{})

	go func() {
		d.workers.Wait()
		close(workersDone)
	}()

	select {
	case <-workersDone:
	case <-ctx.Done():
		d.abort(ctx)
		<-workersDone
	}

	// without workers (never started) the channel still holds its events
	for {
		select {
		case <-d.queue:
			d.dropShutdown.Add(1)

			continue
		default:
		}

		break
	}

	snap := d.state.Load()

	// the retired exporters of an earlier reload are closed already
	for _, m := range snap.active {
		m.state.Store(stateDraining)
	}

	results := d.drainAll(ctx, snap.active)
	d.swap(&dispatchState{retired: append(slices.Clone(snap.retired), snap.active...)})

	report := ShutdownReport{Drained: results, Dropped: d.Status().Queue.Dropped}
	for _, res := range results {
		report.Undelivered += res.Undelivered
	}

	d.report = report

	return report, nil
}

// abort makes the workers drop what they have not delivered: the deadline of the shutdown passed.
func (d *Dispatcher) abort(ctx context.Context) {
	if d.aborted.CompareAndSwap(false, true) {
		d.cfg.Logger.LogAttrs(ctx, slog.LevelWarn, "the shutdown deadline passed: the events still queued are dropped",
			slog.Int("queued", len(d.queue)))
	}
}

// waitCollectors waits until no Collect is between its check of the closed flag and the end of its send, so that
// nothing can enter the channel after the workers drained it. A Collect holds that for a few nanoseconds.
func (d *Dispatcher) waitCollectors(ctx context.Context) {
	for d.inflight.Load() > 0 && ctx.Err() == nil {
		runtime.Gosched()
	}
}
