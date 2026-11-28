package relaycore

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/controlsource"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/segmentstore"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// New builds the core of a validated bootstrap file. The context bounds the build (the exporters and notifiers); it
// does not outlive New. It starts no goroutine of the loops (see [Core.Run]), but it
// opens the state directory and starts the exporters and notifiers, so a core that was built must be stopped with
// [Core.Shutdown]. An error names the setting at fault (see [BuildError]) and never holds a secret. The core
// keeps cfg: the caller must not change it afterwards.
func New(ctx context.Context, cfg *bootstrap.Config, deps *Deps) (*Core, error) {
	if deps == nil {
		deps = &Deps{}
	}

	if cfg == nil {
		return nil, ErrNilConfig
	}

	plane := cfg.ControlPlane
	if plane == nil {
		return nil, &BuildError{Path: "controlPlane", Err: ErrNoControlPlane}
	}

	if _, ok := cfg.Connections[plane.Connection]; !ok {
		return nil, &BuildError{Path: "controlPlane.connection", Err: ErrUnknownConnection}
	}

	// the file asked for these admin keys: the relay never serves with fewer than that
	_, err := flagruntime.ResolveAdminKeys(cfg, deps.Secrets)
	if err != nil {
		return nil, &BuildError{Path: "auth.adminKeys", Err: err}
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	if cfg.Segments != nil {
		logger.LogAttrs(ctx, slog.LevelWarn,
			"segment stores are configured; segment definitions come from the control plane (not active yet)")
	}

	core := &Core{
		log:      logger,
		factory:  connection.New(append([]connection.Option{connection.WithLogger(logger)}, deps.Connection...)...),
		holder:   flagruntime.NewHolder(),
		registry: deps.Registerer,
		runDone:  make(chan struct{}),
		finished: make(chan struct{}),
		released: make(chan struct{}),
	}

	err = core.assemble(ctx, cfg, deps)
	if err != nil {
		core.release(ctx)

		return nil, err
	}

	return core, nil
}

func (c *Core) assemble(ctx context.Context, cfg *bootstrap.Config, deps *Deps) error {
	plane := cfg.ControlPlane

	err := registerConnections(c.factory, cfg, deps.Secrets)
	if err != nil {
		return err
	}

	stores, err := objectStores(c.factory, cfg)
	if err != nil {
		return err
	}

	rcfg := readerConfig(plane)
	if deps.TuneReader != nil {
		deps.TuneReader(rcfg)
	}

	err = checkPolling(rcfg)
	if err != nil {
		return err
	}

	//nolint:contextcheck // the state directory opens without a context
	state, hooks, reason := openStateDir(plane, rcfg, deps, c.log)
	c.state, c.stateDisabled = state, reason

	// the state directory holds what the engine applied: the reader offers its snapshots, the engine decides
	var persist *persister

	if hooks.Save != nil {
		persist = newPersister(hooks.Save, func() uint64 { return c.engine.Taken() })
		hooks.Save = persist.offer
	}

	opts := []manifestreader.Option{
		manifestreader.WithLogger(c.log), manifestreader.WithHooks(hooks), manifestreader.WithObjectStores(stores),
	}
	if deps.Scheduler != nil {
		opts = append(opts, manifestreader.WithScheduler(deps.Scheduler))
	}

	if deps.Jitter != nil {
		opts = append(opts, manifestreader.WithJitter(deps.Jitter))
	}

	c.reader, err = manifestreader.New(stores[plane.Connection], rcfg, opts...)
	if err != nil {
		return &BuildError{Path: "controlPlane.manifest", Err: err}
	}

	c.svc = flagdef.NewService(flagdef.WithLogger(c.log))
	engineHooks := deps.Hooks
	afterRound := engineHooks.AfterRound
	engineHooks.AfterRound = func(res flagruntime.RoundResult) {
		c.reloads.record(&res)
		c.reloads.recordRejected(&res, c.holder.Load().Status.Objects)

		if persist != nil {
			persist.decided(&res, c.holder.Load().Status.Objects)
		}

		if afterRound != nil {
			afterRound(res)
		}
	}

	c.engine = flagruntime.NewEngine(c.log, c.svc, c.holder,
		flagruntime.WithHooks(engineHooks),
		flagruntime.WithSegmentStores(segmentstore.New(cfg)),
		flagruntime.WithSecretOptions(deps.Secrets),
		flagruntime.WithInitialBootstrap(cfg),
	)
	c.source = controlsource.New(c.reader, c.engine, c.holder, c.log)

	return c.assembleEvents(ctx, cfg, deps)
}

// assembleEvents builds the exporters and notifiers and the evaluator that feeds them. It is the last step: the
// exporters are running when it succeeds, and nothing after it can fail.
func (c *Core) assembleEvents(ctx context.Context, cfg *bootstrap.Config, deps *Deps) error {
	c.events = events.NewRuntime(&events.RuntimeConfig{
		Logger:     c.log,
		Broadcast:  deps.Broadcast,
		NewManager: deps.NewManager,
		Builder:    events.Builder{Factory: c.factory, Secrets: deps.Secrets},
	})

	_, err := c.events.Reload(ctx, events.SpecsFrom(cfg))
	if err != nil {
		return &BuildError{Path: "exporters, notifiers", Err: err}
	}

	c.eval = flagruntime.NewEvaluator(c.svc, &flagruntime.EvaluatorConfig{
		EvaluationContextEnrichment: cfg.Evaluation.ContextEnrichment,
		Environment:                 cfg.Evaluation.Environment,
		DisableSemaphoreMetadata:    cfg.Evaluation.DisableSemaphoreMetadata,
		TrackEvent:                  func(event events.Event) { c.events.Collect(event) },
	})

	// the admin keys of the file reach the first runtime through the first round of the engine
	c.engine.Reload(cfg)

	if c.registry != nil {
		c.collector = events.NewCollector(c.events)

		err = c.registry.Register(c.collector)
		if err != nil {
			c.collector = nil

			return &BuildError{Path: "observability.metrics", Err: fmt.Errorf("register the events collector: %w", err)}
		}

		// the reader of the manifest: its polls and the writes of the state directory, with the connection as the one label
		c.readerMetrics, err = metric.NewManifestReader(c.registry, cfg.ControlPlane.Connection, c.readerStatus)
		if err != nil {
			return &BuildError{Path: "observability.metrics", Err: fmt.Errorf("register the reader metrics: %w", err)}
		}
	}

	return nil
}
