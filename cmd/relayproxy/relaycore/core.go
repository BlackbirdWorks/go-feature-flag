package relaycore

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/controlsource"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// Deps are the injected dependencies of a [Core]. Every field is optional.
type Deps struct {
	// Logger receives the logs of every part; nil discards them.
	Logger *slog.Logger
	// Registerer receives the collector of the events; nil registers nothing.
	Registerer prometheus.Registerer
	// Scheduler replaces the timer of the polling loop of the reader (tests drive the rounds with it).
	Scheduler manifestreader.Scheduler
	// Jitter replaces the random source of the reader: it returns a value in [0, 1).
	Jitter func() float64
	// TuneReader changes the configuration of the reader after it was made from the bootstrap file.
	TuneReader func(*manifestreader.Config)
	// StateFS is the file system of the state directory; nil is the real one.
	StateFS statedir.FS
	// NewManager builds the exporter managers, see [events.RuntimeConfig.NewManager]; nil takes the default.
	NewManager events.NewManagerFunc
	// Broadcast is called with every change of a flag set, see [events.RuntimeConfig.Broadcast].
	Broadcast func(flagruntime.Change)
	// Hooks are the hooks of the reconcile engine.
	Hooks flagruntime.Hooks
	// Secrets reads the secrets of the file (environment and files).
	Secrets bootstrap.LoadOptions
	// Connection options for the factory (an HTTP client or round tripper, an observer).
	Connection []connection.Option
}

// Status is the status of a core: the joined status of the reader, the source and the engine, the one of the
// exporters and notifiers, and the state directory. It holds no API key and no secret.
type Status struct {
	// StateDirDisabled is the reason there is no state directory although one was asked for; empty otherwise.
	StateDirDisabled string `json:"stateDirDisabled,omitempty"`
	// Events is the status of the exporters and notifiers.
	Events events.Status `json:"events"`
	// Source is the status of the reader, the engine and the running runtime.
	Source controlsource.Status `json:"source"`
	// Reloads counts the rounds of the engine by outcome since the core was built.
	Reloads ReloadStats `json:"reloads"`
	// ShuttingDown is true from the first call of [Core.Shutdown].
	ShuttingDown bool `json:"shuttingDown"`
}

// ReloadStats counts the rounds of the engine (the bootstrap round, every manifest generation and every reload of
// the file) by outcome.
type ReloadStats struct {
	// LastSuccess is the time of the last applied round, zero when none was applied.
	LastSuccess time.Time `json:"lastSuccess"`
	// Applied, Rejected and Ignored count the rounds by outcome.
	Applied  uint64 `json:"applied"`
	Rejected uint64 `json:"rejected"`
	Ignored  uint64 `json:"ignored"`
}

// reloadCounter keeps the [ReloadStats] with atomics: the engine goroutine writes, Status reads.
type reloadCounter struct {
	applied, rejected, ignored atomic.Uint64
	lastSuccess                atomic.Int64
}

func (r *reloadCounter) record(res flagruntime.RoundResult) {
	switch res.Outcome {
	case flagruntime.OutcomeApplied:
		r.lastSuccess.Store(clock.Now().UnixNano())
		r.applied.Add(1)
	case flagruntime.OutcomeRejected:
		r.rejected.Add(1)
	case flagruntime.OutcomeIgnored:
		r.ignored.Add(1)
	}
}

func (r *reloadCounter) stats() ReloadStats {
	stats := ReloadStats{Applied: r.applied.Load(), Rejected: r.rejected.Load(), Ignored: r.ignored.Load()}
	if nano := r.lastSuccess.Load(); nano != 0 {
		stats.LastSuccess = time.Unix(0, nano).UTC()
	}

	return stats
}

type phase uint8

const (
	phaseNew phase = iota
	phaseRunning
	phaseClosed
)

// Core is the assembled relay core.
type Core struct {
	log       *slog.Logger
	factory   *connection.Factory
	svc       *flagdef.Service
	holder    *flagruntime.Holder
	engine    *flagruntime.Engine
	eval      *flagruntime.Evaluator
	reader    *manifestreader.Reader
	source    *controlsource.Source
	events    *events.Runtime
	state     *statedir.Store
	collector prometheus.Collector
	registry  prometheus.Registerer
	cancel    context.CancelFunc
	// runDone is closed when the loops of Run ended and the state directory was flushed.
	runDone chan struct{}
	// finished is closed when the first Shutdown did its work, with its error in shutdownErr (written before the
	// close).
	finished chan struct{}
	// released is closed when the cleanup ran, exactly once, after the loops ended.
	released    chan struct{}
	shutdownErr error

	stateDisabled string
	reloads       reloadCounter

	// mu guards phase and cancel: the one place the lifecycle is decided.
	mu sync.Mutex
	// releaseOnce runs the cleanup once.
	releaseOnce sync.Once
	// shutdownStarted lets the first Shutdown do the work.
	shutdownStarted atomic.Bool
	phase           phase
}

// Holder returns the holder of the current runtime: handlers load the runtime from it for every request.
func (c *Core) Holder() *flagruntime.Holder { return c.holder }

// Evaluator returns the evaluator of the flag sets. The events of the flags that track them reach the exporters.
func (c *Core) Evaluator() *flagruntime.Evaluator { return c.eval }

// Collect hands an event to the exporters, the way the evaluator does for the events of the flags that track them.
// It reports whether the event was accepted. The event belongs to the exporters from the call: the caller must not
// change it afterwards.
func (c *Core) Collect(event events.Event) bool { return c.events.Collect(event) }

// Status returns a snapshot of the status. It reads atomics and immutable values and never blocks.
func (c *Core) Status() Status {
	return Status{
		StateDirDisabled: c.stateDisabled, Events: c.events.Status(), Source: c.source.Status(),
		Reloads: c.reloads.stats(), ShuttingDown: c.shutdownStarted.Load(),
	}
}
