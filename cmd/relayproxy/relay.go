package main

import (
	"context"
	"crypto/fips140"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"


	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/opentelemetry"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/cmdhelpers/cmdlog"
)

// shutdownTimeout bounds the whole ordered shutdown: the server, the core (the exporters drain within it) and the
// streams.
const shutdownTimeout = 15 * time.Second

// logFormatText is the format of the file that the logger calls logfmt.
const logFormatText = "text"

// exitUsage is the exit code of a command line that does not parse.
const exitUsage = 2

// stopSteps is the number of steps of the shutdown that can fail.
const stopSteps = 3

// errLoopsNotStopped is wrapped by the error of a shutdown whose loops outlived its deadline.
var errLoopsNotStopped = errors.New("the loops of the relay did not stop")

// errMetricsSetup is wrapped by the error of the metrics that can not be set up.
var errMetricsSetup = errors.New("impossible to initialize the prometheus metrics")

// runOptions are the seams of [run]: what the process gives it, and what a test replaces.
type runOptions struct {
	// Stdout receives the version and the banner, Stderr the logs.
	Stdout io.Writer
	Stderr io.Writer
	// OnDraining is called once the readiness answers shutting-down and before the listeners close.
	OnDraining func()
	// Reload asks for a reload of the bootstrap file (SIGHUP).
	Reload <-chan struct{}
	// Load reads the file and the environment.
	Load bootstrap.LoadOptions
	// Deps are the injected dependencies of the core (a test scheduler, a state directory file system); the logger,
	// the metrics registry, the secrets and the broadcast of the streams are set by [run].
	Deps relaycore.Deps
	// Args are the command line arguments, without the program name.
	Args []string
	// ServerOptions are added to the options of the API server (a test gives it its listeners).
	ServerOptions []api.Option
	// ShutdownTimeout bounds the shutdown, [shutdownTimeout] when zero.
	ShutdownTimeout time.Duration
}

// osLoadOptions reads the real environment and files.
func osLoadOptions() bootstrap.LoadOptions {
	return bootstrap.LoadOptions{LookupEnv: os.LookupEnv, ReadFile: os.ReadFile}
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
		_, _ = fmt.Fprint(opts.Stdout, versionText())

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

	_, _ = fmt.Fprintf(opts.Stdout, banner+"\n", version)

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
	// failed is closed when the core or a listener failed after the start.
	failed    chan struct{}
	runCancel context.CancelFunc
	core      *relaycore.Core
	streams   *corehttp.Streams
	ready     *corehttp.Readiness
	server    *api.Server
	log       *slog.Logger
	// loops ends when the core and the server returned.
	loops    sync.WaitGroup
	failOnce sync.Once
}

// start builds the core, the streams and the server from the file and starts them. The core and the server run until
// [relay.wait] stops them. It owns nothing when it returns an error.
func start(ctx context.Context, cfg *bootstrap.Config, logger *slog.Logger, opts *runOptions) (*relay, error) {
	settings, err := corehttp.SettingsFrom(cfg, version)
	if err != nil {
		return nil, fmt.Errorf("server settings: %w", err)
	}

	metrics := &metric.Metrics{}

	if enabled := cfg.Observability.Metrics.Enabled; enabled == nil || *enabled {
		metrics, err = metric.NewMetrics(metric.MetricsOpts{
			Version: version, EnableBulkMetricFlagNames: cfg.Observability.Metrics.PerFlagLabels,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errMetricsSetup, err)
		}
	}

	streams := corehttp.NewStreams() //nolint:contextcheck // the stream workers outlive the context of the start
	deps := opts.Deps
	deps.Logger = logger
	deps.Secrets = opts.Load
	deps.Broadcast = streams.Broadcast

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

	rel := &relay{core: core, streams: streams, ready: corehttp.NewReadiness(core), log: logger,
		failed: make(chan struct{}), runCancel: cancel}

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
		_ = rel.stopAll(ctx, opts)

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

	return rel, nil
}

// fail records that the relay can not go on: wait then stops it and the process exits non-zero.
func (r *relay) fail() { r.failOnce.Do(func() { close(r.failed) }) }

// wait serves until ctx is cancelled (a signal) or the relay failed, applies the reloads meanwhile, then stops in
// order. It returns the exit code.
func (r *relay) wait(ctx context.Context, load func() (*bootstrap.Config, error), opts *runOptions) int {
	code := 0

	for done := false; !done; {
		select {
		case <-ctx.Done():
			done = true
		case <-r.failed:
			code, done = 1, true
		case <-opts.Reload:
			r.reload(ctx, load)
		}
	}

	if r.stopAll(ctx, opts) != nil {
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

// stopAll stops the relay in order and returns the first error: the readiness fails first (the probes answer
// shutting-down while the listeners are still open), then the streams close so that their clients let the server
// drain, the server stops, and the core stops last, draining the exporters. One deadline bounds all of it.
func (r *relay) stopAll(ctx context.Context, opts *runOptions) error {
	timeout := opts.ShutdownTimeout
	if timeout == 0 {
		timeout = shutdownTimeout
	}

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	r.ready.BeginShutdown()

	if opts.OnDraining != nil {
		opts.OnDraining()
	}

	r.log.LogAttrs(stopCtx, slog.LevelInfo, "Stopping API server")

	errs := make([]error, 0, stopSteps)

	errs = append(errs, r.streams.Close(stopCtx))

	if r.server != nil {
		r.server.Stop(stopCtx)
	}

	errs = append(errs, r.core.Shutdown(stopCtx))

	r.runCancel()

	errs = append(errs, r.joinLoops(stopCtx))

	return errors.Join(errs...)
}

// joinLoops waits for the core and the server to return, within the shutdown deadline: the core joins its state
// writer, and a write that the disk does not finish must not keep the process from exiting. What did not stop ends
// with the process.
func (r *relay) joinLoops(ctx context.Context) error {
	stopped := make(chan struct{})

	go func() {
		r.loops.Wait()
		close(stopped)
	}()

	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		r.log.LogAttrs(context.WithoutCancel(ctx), slog.LevelError,
			"the loops of the relay did not stop within the shutdown deadline, the process exits without them")

		return fmt.Errorf("%w: %w", errLoopsNotStopped, ctx.Err())
	}
}
