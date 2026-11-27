package relaycore

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/controlsource"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
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

// Status returns a snapshot of the status. It reads atomics and immutable values and never blocks.
func (c *Core) Status() Status {
	return Status{StateDirDisabled: c.stateDisabled, Events: c.events.Status(), Source: c.source.Status()}
}
