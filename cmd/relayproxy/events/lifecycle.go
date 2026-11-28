package events

import (
	"context"
	"log/slog"
	"maps"
	"runtime"
	"slices"
	"sync"
	"time"
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
	// Cut are the drained entries whose drain passed the deadline. They keep closing in the background.
	Cut []string
}

// DrainResult is the outcome of the drain of one exporter.
type DrainResult struct {
	Name string
	Kind string
	// Undelivered is the number of events that were not delivered when the drain ended: for a drain that was
	// cut, the events that are still pending (they are delivered, or counted as dropped, when the exporter
	// finishes).
	Undelivered int64
	// Cut says that the drain passed the deadline and was abandoned.
	Cut bool
}

// ShutdownReport says what a shutdown drained and dropped.
type ShutdownReport struct {
	// Drained has one entry per exporter that was closed.
	Drained []DrainResult
	// Dropped counts the events dropped before they reached an exporter over the life of the dispatcher, by
	// reason. The drops of an exporter are in its status.
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
// keeps its manager and the events it holds. A new or changed one gets a new manager; the old one of a changed
// or removed exporter is no longer given events and is closed in the background: its hand-off is delivered,
// then its manager is stopped, which flushes it. Apply waits for the closing of the exporters that left, in
// parallel, for the drain timeout of the configuration, which does not depend on ctx: a request that is
// cancelled does not cut the drains. An exporter still closing at the deadline is reported as cut and keeps
// closing; its events are delivered or counted when it finishes. The lifecycle lock is not held while waiting,
// so a shutdown does not wait for a reload.
//
// Events handed over before the swap went to the old exporters and are flushed by their drain; events after it
// go to the new ones; a batch goes to one set only.
func (d *Dispatcher) Apply(ctx context.Context, defs []ExporterDef) (ReloadSet, error) {
	set, leaving, err := d.swapSet(ctx, defs)
	if err != nil {
		return ReloadSet{}, err
	}

	drainCtx, cancel := d.newDrainContext(ctx, d.longestDrainOf(leaving))
	defer cancel()

	d.finishReload(drainCtx, &set, leaving)

	return set, nil
}

// newDrainContext returns the context that bounds a drain: the drain timeout, from now, whatever the context of
// the reload.
func (d *Dispatcher) newDrainContext(
	parent context.Context, timeout time.Duration,
) (context.Context, context.CancelFunc) {
	if d.cfg.drainContext != nil {
		return d.cfg.drainContext(parent)
	}

	return context.WithTimeout(context.WithoutCancel(parent), timeout)
}

// longestDrainOf is the longest drain deadline of the instances: each one is also cut by its own deadline, this one
// only bounds the wait for all of them. With none it is the drain timeout of the configuration.
func (d *Dispatcher) longestDrainOf(list []*managed) time.Duration {
	if len(list) == 0 {
		return d.cfg.DrainTimeout
	}

	var longest time.Duration

	for _, inst := range list {
		longest = max(longest, inst.drain)
	}

	return longest
}

// longestDrain is the longest drain deadline of the exporters that run or are closing: the deadline of a shutdown.
func (d *Dispatcher) longestDrain() time.Duration {
	snap := d.state.Load()

	return d.longestDrainOf(slices.Concat(snap.active, snap.retired))
}

// finishReload waits for the drains of a reload, outside every lock, and records the ones that were cut.
func (d *Dispatcher) finishReload(drainCtx context.Context, set *ReloadSet, leaving []*managed) {
	for _, res := range d.waitClosed(drainCtx, leaving) {
		if res.Cut {
			set.Cut = append(set.Cut, res.Name)
		}
	}
}

// swapSet builds the new set, publishes it and starts the closing of the instances that left, under the
// lifecycle lock.
func (d *Dispatcher) swapSet(ctx context.Context, defs []ExporterDef) (ReloadSet, []*managed, error) {
	d.lifecycle.Lock()
	defer d.lifecycle.Unlock()

	if d.closed.Load() {
		return ReloadSet{}, nil, ErrClosed
	}

	set, leaving := d.swapSetLocked(ctx, defs)

	return set, leaving, nil
}

// swapSetLocked publishes the new set and starts the closing of the instances that left. The caller holds the
// lifecycle lock and has checked that the dispatcher is open.
func (d *Dispatcher) swapSetLocked(ctx context.Context, defs []ExporterDef) (ReloadSet, []*managed) {
	old := d.state.Load()
	current := make(map[string]*managed, len(old.active))

	for _, inst := range old.active {
		current[inst.def.Name] = inst
	}

	var (
		set     ReloadSet
		next    = make([]*managed, 0, len(defs))
		leaving []*managed
	)

	for i := range defs {
		prev := current[defs[i].Name]
		delete(current, defs[i].Name)

		if prev != nil && prev.def.Identity == defs[i].Identity {
			next = append(next, prev)
			set.Kept = append(set.Kept, defs[i].Name)

			continue
		}

		next = append(next, d.launch(&defs[i]))

		if prev == nil {
			set.Added = append(set.Added, defs[i].Name)

			continue
		}

		set.Replaced = append(set.Replaced, defs[i].Name)
		leaving = append(leaving, prev)
	}

	for _, inst := range old.active {
		if _, gone := current[inst.def.Name]; gone {
			set.Removed = append(set.Removed, inst.def.Name)
			leaving = append(leaving, inst)
		}
	}

	for _, inst := range leaving {
		inst.state.Store(stateDraining)
	}

	retired := slices.DeleteFunc(slices.Clone(old.retired), (*managed).isFinished)

	d.swap(&dispatchState{active: next, retired: slices.Concat(retired, leaving)})

	// a request that is cancelled does not cut the drains
	drain := context.WithoutCancel(ctx)

	for _, inst := range leaving {
		inst.startClose(drain)
	}

	return set, leaving
}

// launch builds and starts the instance of an exporter, and records it under its name.
func (d *Dispatcher) launch(def *ExporterDef) *managed {
	names := *d.names.Load()

	entry := names[def.Name]
	if entry == nil {
		entry = &nameEntry{name: def.Name}

		updated := maps.Clone(names)
		updated[def.Name] = entry
		d.names.Store(&updated)
	}

	def.wrapper().name = &entry.stats

	wrap := def.wrapper()
	wrap.pipe, wrap.after, wrap.rnd = def.Pipeline, d.cfg.After, d.cfg.Rand

	mgr := d.cfg.NewManager(def.Name, def.Config, d.cfg.Logger.With(slog.String("exporter", def.Name)))
	mgr.Start()

	inst := newManagedWith(def, mgr, entry, limits{
		after: d.cfg.After, handoff: d.cfg.HandoffSize, batch: d.cfg.BatchSize, drain: d.cfg.DrainTimeout,
	}, d.cfg.Logger)
	entry.latest.Store(inst)

	return inst
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

// waitClosed waits for the instances to finish closing until the deadline of ctx. An instance still closing at
// the deadline is cut: it keeps closing in the background.
func (d *Dispatcher) waitClosed(ctx context.Context, list []*managed) []DrainResult {
	out := make([]DrainResult, len(list))

	var wg sync.WaitGroup

	for i, inst := range list {
		wg.Go(func() { out[i] = d.awaitInstance(ctx, inst) })
	}

	wg.Wait()

	return out
}

func (d *Dispatcher) awaitInstance(ctx context.Context, inst *managed) DrainResult {
	res := DrainResult{Name: inst.def.Name, Kind: inst.def.Kind}

	// an instance that finished is not cut, whatever the deadline: with both ready, select would pick at random
	if inst.isFinished() {
		res.Undelivered = asInt(inst.undelivered.Load())

		return res
	}

	select {
	case <-inst.finished:
		res.Undelivered = asInt(inst.undelivered.Load())
	case <-ctx.Done():
		d.cut(ctx, inst, &res)
	case <-inst.haltDone():
		d.cut(ctx, inst, &res)
	}

	return res
}

// cut records that the drain of the instance passed its deadline: it keeps closing in the background.
func (d *Dispatcher) cut(ctx context.Context, inst *managed, res *DrainResult) {
	// the end of the drain deadline of an instance that has just finished is not a cut
	if inst.isFinished() {
		res.Undelivered = asInt(inst.undelivered.Load())

		return
	}

	res.Cut = true
	res.Undelivered = asInt(inst.pending())

	inst.state.CompareAndSwap(stateDraining, stateCut)
	d.cfg.Logger.LogAttrs(ctx, slog.LevelWarn, "exporter drain passed the deadline",
		slog.String("exporter", inst.def.Name), slog.Int64("pending", res.Undelivered))
}

// asInt converts an event count: counts do not reach 2^63.
func asInt(count uint64) int64 { return int64(count) } //nolint:gosec // event counts do not reach 2^63

// Done is closed when [Dispatcher.Shutdown] finished.
func (d *Dispatcher) Done() <-chan struct{} { return d.done }

// Shutdown stops taking events, delivers what the channel holds to the exporters, closes every exporter (which
// flushes it once) and reports what was drained and dropped. The deadline of ctx bounds the whole: past it the
// events still in the channel are dropped and counted, and an exporter that is still closing is cut and keeps
// closing in the background (see [Dispatcher.WaitDrains]). After it, Collect drops and counts. A second call
// does nothing and returns the first report. Apart from an exporter that was cut, no goroutine of the
// dispatcher is left.
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

	// an instance that left in an earlier reload may still be closing: it is waited for too, once
	closing := slices.Concat(snap.active, slices.DeleteFunc(slices.Clone(snap.retired), (*managed).isFinished))

	for _, inst := range snap.active {
		inst.state.CompareAndSwap(stateRunning, stateDraining)
		inst.startClose(ctx)
	}

	// the draining instances are published as retired before they are waited for: a WaitDrains that runs
	// meanwhile waits for them too
	d.swap(&dispatchState{retired: slices.Concat(snap.retired, snap.active)})

	results := d.waitClosed(ctx, closing)

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
