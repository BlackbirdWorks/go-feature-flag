package events

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/internal"
)

// DefaultNotifierQueueSize is the number of changes that wait for one notifier.
const DefaultNotifierQueueSize = 64

// NotificationsConfig configures [Notifications]. Zero values take the defaults.
type NotificationsConfig struct {
	Logger       *slog.Logger
	Broadcast    func(flagruntime.Change)
	drainContext func(parent context.Context) (context.Context, context.CancelFunc)
	Builder      Builder
	QueueSize    int
	DrainTimeout time.Duration
}

// Notifications is the change worker: one goroutine consumes the changes of the reconcile engine and offers
// each of them to the bounded queue of every notifier. Each notifier has one goroutine that calls it, one change
// at a time and in the order of the queue, so the changes of a flag set reach a notifier in order. A slow or
// failing notifier fills its own queue and loses its own changes; it never delays the consumer or the other
// notifiers.
//
// There is no per-call timeout: the notifiers bound their own HTTP calls (the Teams notifier 5 seconds, the
// others the timeout of the HTTP client of the engine), and a call that never returns holds only the goroutine
// of its notifier.
//
// A reload that replaces a notifier keeps the order: the new instance accepts changes at once, but calls its
// notifier only after the old instance delivered what its queue held (or the drain deadline passed).
type Notifications struct {
	state atomic.Pointer[notifyState]
	// names is the table of the notifier names ever used; its entries keep the counters across reloads.
	names atomic.Pointer[map[string]*notifierName]
	stop  chan struct{}
	done  chan struct{}
	// unwatch removes the shutdown that the end of the context of Start would run.
	unwatch  func() bool
	cfg      NotificationsConfig
	consumer sync.WaitGroup
	// mu guards the offer of a change (read) against the swap of the set (write).
	mu sync.RWMutex
	// lifecycle serialises Start, the swap of Apply and Shutdown. It is not held while waiting for a drain.
	lifecycle sync.Mutex
	started   bool
	closed    bool
}

type notifyState struct {
	active  []*notifierRun
	retired []*notifierRun
}

// NewNotifications returns a worker with no notifier.
func NewNotifications(config *NotificationsConfig) *Notifications {
	cfg := *config
	cfg.Logger = internal.LoggerOrDiscard(cfg.Logger)

	if cfg.QueueSize <= 0 {
		cfg.QueueSize = DefaultNotifierQueueSize
	}

	if cfg.DrainTimeout <= 0 {
		cfg.DrainTimeout = DefaultDrainTimeout
	}

	notes := &Notifications{cfg: cfg, stop: make(chan struct{}), done: make(chan struct{})}
	notes.state.Store(&notifyState{})
	notes.names.Store(&map[string]*notifierName{})

	return notes
}

// Start starts the consumer of changes (a nil channel is allowed: nothing is consumed). The consumer stops when
// the channel is closed or at Shutdown. The end of the context shuts the notifications down like
// [Notifications.Shutdown], with the drain timeout of the configuration as its deadline.
func (n *Notifications) Start(ctx context.Context, changes <-chan flagruntime.Change) error {
	n.lifecycle.Lock()
	defer n.lifecycle.Unlock()

	if n.closed {
		return ErrClosed
	}

	if n.started {
		return ErrAlreadyStarted
	}

	n.started = true

	n.consumer.Add(1)

	go n.consume(ctx, changes)

	n.unwatch = context.AfterFunc(ctx, func() {
		drain, cancel := context.WithTimeout(context.WithoutCancel(ctx), n.cfg.DrainTimeout)
		defer cancel()

		_ = n.Shutdown(drain)
	})

	return nil
}

// Reload builds the notifiers of the specs and applies them, see [Notifications.Apply].
func (n *Notifications) Reload(ctx context.Context, specs Specs) (ReloadSet, error) {
	defs, err := n.cfg.Builder.Notifiers(ctx, specs)
	if err != nil {
		return ReloadSet{}, err
	}

	return n.Apply(ctx, defs)
}

// Apply replaces the set of notifiers in place. A notifier whose identity did not change keeps its goroutine
// and its queue. A new or changed one gets its own; the old one of a changed or removed notifier gets no more
// changes, delivers what its queue holds within the drain timeout (which does not depend on ctx) and stops.
// The lifecycle lock is not held while waiting.
func (n *Notifications) Apply(ctx context.Context, defs []NotifierDef) (ReloadSet, error) {
	set, leaving, err := n.swapSet(ctx, defs)
	if err != nil {
		return ReloadSet{}, err
	}

	drainCtx, cancel := n.newDrainContext(ctx)
	defer cancel()

	n.finishReload(drainCtx, &set, leaving)

	return set, nil
}

// closeRuns waits for the runs (already closed) until the deadline of ctx. It returns the names of the runs that
// were cut.
func closeRuns(ctx context.Context, runs []*notifierRun) []string {
	var cut []string

	for _, run := range runs {
		if !run.wait(ctx) {
			cut = append(cut, run.def.Name)
		}
	}

	return cut
}

// WaitDrains waits until every notifier instance that left the set has finished, including the ones whose drain was
// cut. The instances that are still in the set are not waited for. It returns the error of ctx when that comes first.
func (n *Notifications) WaitDrains(ctx context.Context) error {
	for _, run := range n.state.Load().retired {
		select {
		case <-run.finished:
		case <-ctx.Done():
			return ctx.Err() //nolint:wrapcheck // the context error says it
		}
	}

	return nil
}

// Done is closed when [Notifications.Shutdown] finished.
func (n *Notifications) Done() <-chan struct{} { return n.done }

// Shutdown stops consuming changes, lets every notifier deliver what its queue holds until the deadline of ctx,
// and stops them. What is still queued at the deadline is dropped and counted. A second call does nothing.
func (n *Notifications) Shutdown(ctx context.Context) error {
	n.lifecycle.Lock()
	defer n.lifecycle.Unlock()

	if n.closed {
		return nil
	}

	n.closed = true
	defer close(n.done)

	if n.unwatch != nil {
		n.unwatch()
	}

	close(n.stop)
	n.consumer.Wait()

	snap := n.state.Load()

	for _, run := range snap.active {
		run.close()
	}

	// the draining runs are published as retired before they are waited for: a WaitDrains that runs meanwhile
	// waits for them too
	n.swap(&notifyState{retired: slices.Concat(snap.retired, snap.active)})

	closeRuns(ctx, slices.Concat(snap.active, snap.retired))

	return nil
}

// Status returns a snapshot of the notifiers by name, with their cumulative counters, without blocking.
func (n *Notifications) Status() Status {
	names := *n.names.Load()
	out := Status{Notifiers: make([]NotifierStatus, 0, len(names))}

	for _, name := range slices.Sorted(maps.Keys(names)) {
		out.Notifiers = append(out.Notifiers, names[name].status())
	}

	return out
}

func (n *Notifications) consume(ctx context.Context, changes <-chan flagruntime.Change) {
	defer n.consumer.Done()

	for {
		select {
		case change, ok := <-changes:
			if !ok {
				return
			}

			n.dispatch(ctx, change)
		case <-ctx.Done():
			return
		case <-n.stop:
			return
		}
	}
}

// dispatch hands one change to the broadcast hook and to every notifier.
func (n *Notifications) dispatch(ctx context.Context, change flagruntime.Change) {
	if !change.Diff.HasDiff() {
		return
	}

	if n.cfg.Broadcast != nil {
		n.broadcast(ctx, change)
	}

	// the public flag set is a view for the streams of the clients without a key: the flags of its sources are
	// notified under their own flag set, once, and a flag set that became private deletes nothing
	if change.FlagSet == flagruntime.PublicName {
		return
	}

	n.mu.RLock()
	defer n.mu.RUnlock()

	for _, run := range n.state.Load().active {
		run.offer(change)
	}
}

func (n *Notifications) broadcast(ctx context.Context, change flagruntime.Change) {
	defer func() {
		if r := recover(); r != nil {
			n.cfg.Logger.LogAttrs(ctx, slog.LevelError, "the broadcast hook panicked",
				slog.String("flagSet", change.FlagSet), slog.String("type", fmt.Sprintf("%T", r)))
		}
	}()

	n.cfg.Broadcast(change)
}

func (n *Notifications) swapSet(ctx context.Context, defs []NotifierDef) (ReloadSet, []*notifierRun, error) {
	n.lifecycle.Lock()
	defer n.lifecycle.Unlock()

	if n.closed {
		return ReloadSet{}, nil, ErrClosed
	}

	set, leaving := n.swapSetLocked(ctx, defs)

	return set, leaving, nil
}

// swapSetLocked publishes the new set and closes the instances that left. The caller holds the lifecycle lock and
// has checked that the notifications are open.
func (n *Notifications) swapSetLocked(ctx context.Context, defs []NotifierDef) (ReloadSet, []*notifierRun) {
	old := n.state.Load()
	current := make(map[string]*notifierRun, len(old.active))

	for _, run := range old.active {
		current[run.def.Name] = run
	}

	var (
		set     ReloadSet
		next    = make([]*notifierRun, 0, len(defs))
		leaving []*notifierRun
	)

	for _, def := range defs {
		prev := current[def.Name]
		delete(current, def.Name)

		if prev != nil && prev.def.Identity == def.Identity {
			next = append(next, prev)
			set.Kept = append(set.Kept, def.Name)

			continue
		}

		next = append(next, n.launch(ctx, def, prev))

		if prev == nil {
			set.Added = append(set.Added, def.Name)

			continue
		}

		set.Replaced = append(set.Replaced, def.Name)
		leaving = append(leaving, prev)
	}

	for _, run := range old.active {
		if _, gone := current[run.def.Name]; gone {
			set.Removed = append(set.Removed, run.def.Name)
			leaving = append(leaving, run)
		}
	}

	retired := slices.DeleteFunc(slices.Clone(old.retired), (*notifierRun).isFinished)

	n.swap(&notifyState{active: next, retired: slices.Concat(retired, leaving)})

	for _, run := range leaving {
		run.close()
	}

	return set, leaving
}

// finishReload waits for the drains of a reload, outside every lock, and records the ones that were cut.
func (*Notifications) finishReload(drainCtx context.Context, set *ReloadSet, leaving []*notifierRun) {
	set.Cut = closeRuns(drainCtx, leaving)
}

// launch starts the instance of a notifier, after prev (the instance it replaces) in the order of the calls.
func (n *Notifications) launch(ctx context.Context, def NotifierDef, prev *notifierRun) *notifierRun {
	names := *n.names.Load()

	entry := names[def.Name]
	if entry == nil {
		entry = &notifierName{name: def.Name}

		updated := maps.Clone(names)
		updated[def.Name] = entry
		n.names.Store(&updated)
	}

	run := newNotifierRun(ctx, def, entry, prev, &n.cfg)
	entry.latest.Store(run)

	return run
}

func (n *Notifications) swap(st *notifyState) {
	n.mu.Lock()
	n.state.Store(st)
	n.mu.Unlock()
}

// newDrainContext returns the context that bounds a drain: the drain timeout, from now, whatever the context of
// the reload.
func (n *Notifications) newDrainContext(parent context.Context) (context.Context, context.CancelFunc) {
	if n.cfg.drainContext != nil {
		return n.cfg.drainContext(parent)
	}

	return context.WithTimeout(context.WithoutCancel(parent), n.cfg.DrainTimeout)
}
