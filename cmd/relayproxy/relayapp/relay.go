// Package relayapp wires and runs the relay proxy: the command line, the start, the reloads and the ordered stop.
// The main package of the binary only builds the signal context and calls [Main].
package relayapp

import (
	"context"
	"crypto/fips140"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/opentelemetry"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/cmdhelpers/cmdlog"
)

// logFormatText is the format of the file that the logger calls logfmt.
const logFormatText = "text"

// exitUsage is the exit code of a command line that does not parse.
const exitUsage = 2

// stopSteps is the number of steps of the shutdown that can fail.
const stopSteps = 4

// errLoopsNotStopped is wrapped by the error of a shutdown whose loops outlived its deadline.
var errLoopsNotStopped = errors.New("the loops of the relay did not stop")

// errMetricsSetup is wrapped by the error of the metrics that can not be set up.
var errMetricsSetup = errors.New("impossible to initialize the prometheus metrics")

// runOptions are the seams of [run]: what the process gives it, and what a test replaces.
type runOptions struct {
	// Stdout receives the version and the banner, Stderr the logs.
	Stdout io.Writer
	Stderr io.Writer
	// OnDraining is called with the context of the shutdown once the readiness answers shutting-down and before the
	// drain delay starts.
	OnDraining func(ctx context.Context)
	// OnStarted is called with the relay once its parts run; a test uses it to reach them.
	OnStarted func(*relay)
	// ShutdownContext returns the context of the shutdown, which must not be derived from the cancelled context of
	// the run. It ends when the relay is told to stop hard (the second signal). The context of a new signal
	// watcher when nil.
	ShutdownContext func(parent context.Context) (context.Context, context.CancelFunc)
	// DrainWait waits for the drain delay or for ctx; a test replaces it with a gate. The timer of the process
	// when nil.
	DrainWait func(ctx context.Context, delay time.Duration) error
	// WithTimeout bounds the stop that follows the drain delay, like [context.WithTimeout]; a test replaces it
	// with a context that it ends itself.
	WithTimeout func(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc)
	// Reload asks for a reload of the bootstrap file (SIGHUP).
	Reload <-chan struct{}
	// Load reads the file and the environment.
	Load bootstrap.LoadOptions
	// Deps are the injected dependencies of the core (a test scheduler, a state directory file system); the logger,
	// the metrics registry, the secrets and the broadcast of the streams are set by [run].
	Deps relaycore.Deps
	// Build is the version, commit and date stamped by the linker.
	Build Build
	// Args are the command line arguments, without the program name.
	Args []string
	// ServerOptions are added to the options of the API server (a test gives it its listeners).
	ServerOptions []api.Option
}

// announce tells the hook of a test that the relay runs.
func (o *runOptions) announce(rel *relay) {
	if o.OnStarted != nil {
		o.OnStarted(rel)
	}
}

const banner = `█▀▀ █▀█   █▀▀ █▀▀ ▄▀█ ▀█▀ █ █ █▀█ █▀▀   █▀▀ █   ▄▀█ █▀▀
█▄█ █▄█   █▀  ██▄ █▀█  █  █▄█ █▀▄ ██▄   █▀  █▄▄ █▀█ █▄█

     █▀█ █▀▀ █   ▄▀█ █▄█   █▀█ █▀█ █▀█ ▀▄▀ █▄█
     █▀▄ ██▄ █▄▄ █▀█  █    █▀▀ █▀▄ █▄█ █ █  █ 

Semaphore Relay Proxy - Version %s
_____________________________________________`

// Main runs the relay on the environment that lookupEnv reads (the process reads it only in main.go) until ctx is
// cancelled (SIGTERM, SIGINT) and returns the exit code.
// SIGHUP reloads the bootstrap file.
func Main(
	ctx context.Context, args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool), build Build,
) int {
	return mainWith(ctx, args, stdout, stderr, lookupEnv, build, nil)
}

// mainWith is [Main] with a function that changes the options of the run (a test gives it its listener and its
// contexts).
func mainWith(
	ctx context.Context, args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool), build Build,
	tweak func(*runOptions),
) int {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)

	defer signal.Stop(hup)

	reload := make(chan struct{}, 1)

	// the forwarder ends with its own context and is joined before Main returns
	fwdCtx, stopForward := context.WithCancel(ctx)

	var forwarder sync.WaitGroup

	forwarder.Go(func() { forwardSignals(fwdCtx, hup, reload) })

	defer func() {
		stopForward()
		forwarder.Wait()
	}()

	opts := &runOptions{
		Args:   args,
		Stdout: stdout,
		Stderr: stderr,
		Build:  build,
		Load:   bootstrap.LoadOptions{LookupEnv: lookupEnv, ReadFile: os.ReadFile},
		Reload: reload,
	}
	if tweak != nil {
		tweak(opts)
	}

	return run(ctx, opts)
}

// hardStop returns the shutdown context: it is not derived from runCtx, and it ends when a stop request comes after
// the first one. The first request is the one that ends runCtx (a signal), or an internal failure that started the
// shutdown before any signal; the watcher of the next request (newSignals) starts as soon as runCtx ends, so that
// no request is lost while the relay is busy. release ends the context and joins the watcher; armed waits for the
// watcher.
func hardStop(
	runCtx context.Context, newSignals func(context.Context) (context.Context, context.CancelFunc),
) (context.Context, func(), func()) {
	hard, endHard := context.WithCancel(context.WithoutCancel(runCtx))
	watcherDone := make(chan struct{})
	armedCh := make(chan struct{})

	stopWatching := context.AfterFunc(runCtx, func() {
		defer close(watcherDone)

		next, stop := newSignals(runCtx)
		defer stop()

		close(armedCh)

		select {
		case <-next.Done():
			endHard()
		case <-hard.Done():
		}
	})

	release := func() {
		endHard()

		if !stopWatching() {
			<-watcherDone // the watcher started: it ends now that hard is done
		}
	}

	// armed waits until the watcher of the next request exists, when runCtx has ended (the watcher is then on its
	// way): the shutdown starts with the next request already watched.
	armed := func() {
		if runCtx.Err() != nil {
			<-armedCh
		}
	}

	return hard, release, armed
}

// signalContext watches the next SIGINT or SIGTERM: it ends when one arrives.
func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.WithoutCancel(parent), os.Interrupt, syscall.SIGTERM)
}

// forwardSignals turns every signal of in into a reload request: requests that arrive while one waits are merged.
func forwardSignals(ctx context.Context, in <-chan os.Signal, out chan<- struct{}) {
	for {
		select {
		case <-in:
			select {
			case out <- struct{}{}:
			default:
			}
		case <-ctx.Done():
			return
		}
	}
}

// run starts the relay proxy on a version 2 bootstrap file and returns the exit code of the process. A cancelled ctx
// is the request to stop (SIGTERM): the readiness fails first, then the streams and the listeners close, then the
// core drains. A file that is not valid, or is not version 2, starts nothing.
func run(ctx context.Context, opts *runOptions) int {
	logger := cmdlog.NewDefault(cmdlog.WithWriter(opts.Stderr))

	// one strict parse decides: --version does not excuse an unknown flag, and --help wins over --version
	cmdline, err := parseCommandLine(opts.Args)
	if err != nil {
		_, _ = fmt.Fprintf(opts.Stderr, "semaphore: %v\n\n%s", err, usageText())

		return exitUsage
	}

	if cmdline.Help {
		_, _ = fmt.Fprint(opts.Stdout, usageText())

		return 0
	}

	if cmdline.Version {
		_, _ = fmt.Fprint(opts.Stdout, versionText(opts.Build))

		return 0
	}

	overrides := cmdline.Overrides

	cfg, err := bootstrap.Load(overrides, opts.Load)
	if err != nil {
		logFatal(ctx, logger.Slog, "configuration error", cmdlog.Err(err))

		return 1
	}

	err = logger.Update(logFormat(cfg.Observability.Logging.Format), cfg.Observability.Logging.Level)
	if err != nil {
		logFatal(ctx, logger.Slog, "configuration error", cmdlog.Err(err))

		return 1
	}

	if !cfg.Server.HideBanner {
		_, _ = fmt.Fprintf(opts.Stdout, banner+"\n", opts.Build.Version)
	}

	logger.Slog.LogAttrs(ctx, slog.LevelInfo, "crypto mode", slog.Bool("fips140", fips140.Enabled()))

	rel, err := start(ctx, cfg, logger.Slog, opts)
	if err != nil {
		logFatal(ctx, logger.Slog, "impossible to start Semaphore", cmdlog.Err(err))

		return 1
	}

	return rel.wait(ctx, loaderOf(overrides, opts.Load), opts)
}

// loaderOf reads the file again with the overrides of the command line: a reload applies the same flags as the start.
func loaderOf(overrides *bootstrap.Overrides, load bootstrap.LoadOptions) func() (*bootstrap.Config, error) {
	return func() (*bootstrap.Config, error) { return bootstrap.Load(overrides, load) }
}

// logFormat maps the format of the file to the one of the logger: "text" is logfmt.
func logFormat(format string) string {
	if format == logFormatText {
		return "logfmt"
	}

	return format
}

// logFatal logs the message at the fatal level.
func logFatal(ctx context.Context, logger *slog.Logger, msg string, attrs ...slog.Attr) {
	logger.LogAttrs(ctx, cmdlog.LevelFatal, "fatal error in relay proxy",
		append([]slog.Attr{slog.String("reason", msg)}, attrs...)...)
}

// relay is a started relay: the core, the streams and the HTTP server.
type relay struct {
	log       *slog.Logger
	drainWait func(ctx context.Context, delay time.Duration) error
	core      *relaycore.Core
	streams   *corehttp.Streams
	ready     *corehttp.Readiness
	server    *api.Server
	runCancel context.CancelFunc
	loopsDone chan struct{}
	// failedCtx ends when the core or a listener failed after the start; fail ends it.
	failedCtx   context.Context //nolint:containedctx // the failure of the relay, read by wait
	fail        context.CancelFunc
	onDraining  func(ctx context.Context)
	withTimeout func(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc)
	loops       sync.WaitGroup
	timeout     time.Duration
	drainDelay  time.Duration
}

// start builds the core, the streams and the server from the file and starts them. The core and the server run until
// [relay.wait] stops them. It owns nothing when it returns an error.
func start(ctx context.Context, cfg *bootstrap.Config, logger *slog.Logger, opts *runOptions) (*relay, error) {
	settings, err := corehttp.SettingsFrom(cfg, opts.Build.Version)
	if err != nil {
		return nil, fmt.Errorf("server settings: %w", err)
	}

	metrics := &metric.Metrics{}

	if enabled := cfg.Observability.Metrics.Enabled; enabled == nil || *enabled {
		metrics, err = metric.NewMetrics(metric.MetricsOpts{
			Version: opts.Build.Version, EnableBulkMetricFlagNames: cfg.Observability.Metrics.PerFlagLabels,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errMetricsSetup, err)
		}
	}

	streams := corehttp.NewStreams(ctx) // the workers outlive the start: only their profiler labels come from ctx
	deps := opts.Deps
	deps.Logger = logger
	deps.Secrets = opts.Load
	wireStreams(&deps, streams)

	if metrics.Enabled() {
		deps.Registerer = metrics.Registry
	}

	// the lifetime of the core and of the server ends with their own stop, not with the signal
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	core, err := relaycore.New(runCtx, cfg, &deps)
	if err != nil {
		cancel()

		_ = streams.Close(runCtx)

		return nil, fmt.Errorf("core: %w", err)
	}

	failedCtx, fail := context.WithCancel(context.WithoutCancel(ctx))

	rel := &relay{
		core:        core,
		streams:     streams,
		ready:       corehttp.NewReadiness(core),
		log:         logger,
		failedCtx:   failedCtx,
		fail:        fail,
		runCancel:   cancel,
		drainWait:   opts.DrainWait,
		withTimeout: opts.WithTimeout,
		onDraining:  opts.OnDraining,
		drainDelay: shutdownDelay(
			cfg.Server.Shutdown.DrainDelay,
		),
		timeout: shutdownTimeoutOf(cfg.Server.Shutdown.Timeout),
	}

	// the OpenTelemetry endpoint and the global tracer provider are process-wide: they are set here only
	opentelemetry.ExportEnvironment(&settings.Telemetry)

	serverOpts := append([]api.Option{api.WithGlobalTelemetry(), api.WithExit(rel.fail)}, opts.ServerOptions...)

	serverCfg := &corehttp.Config{
		Settings: &settings, Logger: logger, Streams: streams, Metrics: metrics, Readiness: rel.ready,
	}

	server, err := corehttp.New( //nolint:contextcheck // Stop ends the server, not a context
		core,
		serverCfg,
		serverOpts...)
	if err != nil {
		rel.drainDelay = 0 // the start failed: there is no traffic to drain

		_ = rel.Shutdown(context.WithoutCancel(ctx))

		return nil, fmt.Errorf("server: %w", err)
	}

	rel.server = &server

	rel.loops.Go(func() {
		runErr := core.Run(runCtx)
		if runErr != nil {
			logger.LogAttrs(runCtx, slog.LevelError, "the core stopped", cmdlog.Err(runErr))
			rel.fail()
		}
	})
	rel.loops.Go(func() { server.StartWithContext(runCtx) })

	rel.loopsDone = make(chan struct{})

	go func() {
		rel.loops.Wait()
		close(rel.loopsDone)
	}()

	opts.announce(rel)

	return rel, nil
}

// Shutdown stops the relay in order and returns the errors of the steps. ctx is the shutdown context: it is not
// derived from the signal context, and ending it (the second signal) stops the relay hard.
//
//  1. the readiness answers 503 shutting-down while the listeners stay open;
//  2. the drain delay: the relay keeps serving, so that the load balancers stop sending traffic;
//  3. from here the timeout bounds the rest: the change streams close, so that their clients let the server drain;
//  4. the HTTP servers stop gracefully, the requests in flight finish;
//  5. the core stops: the event queues are flushed and the state is written.
//
// When the timeout ends first the servers close, the requests still in flight are cut and the error says so.
func (r *relay) Shutdown(ctx context.Context) error {
	r.ready.BeginShutdown()
	r.log.LogAttrs(ctx, slog.LevelInfo, "shutdown: readiness off, /readyz answers 503")

	if r.onDraining != nil {
		r.onDraining(ctx)
	}

	r.drain(ctx)

	withTimeout := r.withTimeout
	if withTimeout == nil {
		withTimeout = context.WithTimeout
	}

	stopCtx, cancel := withTimeout(ctx, r.timeout)
	defer cancel()

	errs := make([]error, 0, stopSteps)

	errs = append(
		errs,
		r.closeStreams(stopCtx),
	)

	if r.server != nil {
		errs = append(
			errs,
			r.stopServer(stopCtx),
		)
	}

	errs = append(errs, r.stopCore(stopCtx))

	r.runCancel()

	errs = append(errs, r.joinLoops(stopCtx))

	err := errors.Join(errs...)
	if err != nil {
		r.log.LogAttrs(
			context.WithoutCancel(ctx),
			slog.LevelError,
			"shutdown: stopped hard, what was cut is listed above",
			cmdlog.Err(err),
		)
	}

	return err
}

// shutdownDelay is the value of an optional duration of the file; unset is zero.
func shutdownDelay(d *bootstrap.Delay) time.Duration {
	if d == nil {
		return 0
	}

	return time.Duration(*d)
}

// shutdownTimeoutOf is the timeout of the file, or the default when it is unset.
func shutdownTimeoutOf(d *bootstrap.Duration) time.Duration {
	if d == nil {
		return bootstrap.DefaultShutdownTimeout
	}

	return time.Duration(*d)
}

// wait serves until ctx is cancelled (a signal) or the relay failed, applies the reloads meanwhile, then shuts down.
// The shutdown runs on a new context that the signal does not cancel: only the second signal does (it is the
// context of a new signal watcher), and that is the hard stop. It returns the exit code.
func (r *relay) wait(ctx context.Context, load func() (*bootstrap.Config, error), opts *runOptions) int {
	code := 0

	newSignals := opts.ShutdownContext
	if newSignals == nil {
		newSignals = signalContext
	}

	// the watcher of the second stop request starts the moment the run context ends, whatever the loop is doing
	shutdownCtx, release, armed := hardStop(ctx, newSignals)
	defer release()

	for done := false; !done; {
		select {
		case <-ctx.Done():
			done = true
		case <-r.failedCtx.Done():
			// nothing serves any more: the drain delay would only keep a failed relay alive
			r.drainDelay = 0
			code, done = 1, true
		case <-opts.Reload:
			r.reload(ctx, load)
		}
	}

	armed()

	if r.Shutdown(shutdownCtx) != nil {
		code = 1
	}

	return code
}

// reload applies the file again: only what the core says is live changes, the rest is named in the status.
func (r *relay) reload(ctx context.Context, load func() (*bootstrap.Config, error)) {
	cfg, err := load()
	if err == nil {
		err = r.core.Reload(cfg)
	}

	if err != nil {
		r.log.LogAttrs(
			ctx,
			slog.LevelError,
			"the reload of the bootstrap file was refused, the relay keeps its settings",
			cmdlog.Err(err),
		)

		return
	}

	r.log.LogAttrs(
		ctx,
		slog.LevelInfo,
		"reload queued: the outcome is logged by the engine (runtime reload) and shown in the status",
	)
}

// drain keeps serving for the drain delay: it ends early when ctx ends (the second signal).
func (r *relay) drain(ctx context.Context) {
	if r.drainDelay <= 0 || ctx.Err() != nil {
		return // no delay, or the shutdown is already a hard stop
	}

	r.log.LogAttrs(ctx, slog.LevelInfo, "shutdown: draining, still serving", slog.Duration("drainDelay", r.drainDelay))

	wait := r.drainWait
	if wait == nil {
		wait = sleepContext
	}

	err := wait(ctx, r.drainDelay)
	if err != nil {
		r.log.LogAttrs(
			context.WithoutCancel(ctx),
			slog.LevelWarn,
			"shutdown: the drain delay was cut short",
			cmdlog.Err(err),
		)
	}
}

// sleepContext waits for d or for ctx.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("drain: %w", ctx.Err())
	}
}

// closeStreams is the step that closes the change streams.
func (r *relay) closeStreams(ctx context.Context) error {
	err := r.streams.Close(ctx)
	if err != nil {
		r.log.LogAttrs(
			context.WithoutCancel(ctx),
			slog.LevelError,
			"shutdown: change streams did not close",
			cmdlog.Err(err),
		)

		return fmt.Errorf("streams: %w", err)
	}

	r.log.LogAttrs(ctx, slog.LevelInfo, "shutdown: change streams closed")

	return nil
}

// stopServer is the step that stops the HTTP servers gracefully.
func (r *relay) stopServer(ctx context.Context) error {
	err := r.server.Shutdown(ctx)
	if err != nil {
		r.log.LogAttrs(context.WithoutCancel(ctx), slog.LevelError, "shutdown: API server did not stop gracefully",
			cmdlog.Err(err))

		return fmt.Errorf("api server: %w", err)
	}

	r.log.LogAttrs(ctx, slog.LevelInfo, "shutdown: API server stopped")

	return nil
}

// stopCore is the step that flushes the event queues, stops the engine and writes the state.
func (r *relay) stopCore(ctx context.Context) error {
	err := r.core.Shutdown(ctx)
	if err != nil {
		r.log.LogAttrs(
			context.WithoutCancel(ctx),
			slog.LevelError,
			"shutdown: core did not stop cleanly",
			cmdlog.Err(err),
		)

		return fmt.Errorf("core: %w", err)
	}

	r.log.LogAttrs(ctx, slog.LevelInfo, "shutdown: core stopped, events flushed and state written")

	return nil
}

// joinLoops waits for the core and the server to return, within the shutdown deadline: the core joins its state
// writer, and a write that the disk does not finish must not keep the process from exiting. What did not stop ends
// with the process.
func (r *relay) joinLoops(ctx context.Context) error {
	if r.loopsDone == nil {
		return nil // nothing was started
	}

	select {
	case <-r.loopsDone:
		return nil
	case <-ctx.Done():
		r.log.LogAttrs(context.WithoutCancel(ctx), slog.LevelError,
			"the loops of the relay did not stop within the shutdown deadline, the process exits without them")

		return fmt.Errorf("%w: %w", errLoopsNotStopped, ctx.Err())
	}
}

// wireStreams makes the core feed the streams: it broadcasts the changes, and after every swap it closes the streams of
// the clients that the new runtime does not entitle anymore, before the changes of the swap are queued.
func wireStreams(deps *relaycore.Deps, streams *corehttp.Streams) {
	before := deps.Broadcast
	deps.Broadcast = func(change flagruntime.Change) {
		if before != nil {
			before(change)
		}

		streams.Broadcast(change)
	}

	next := deps.Hooks.AfterSwap
	deps.Hooks.AfterSwap = func(rt *flagruntime.Runtime) {
		streams.Revalidate(rt)

		if next != nil {
			next(rt)
		}
	}
}
