package ffclient

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"runtime"
	"time"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/evaluation"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/modules/core/model"
)

const (
	// The number of workers is half the CPUs, at most maxWorkers.
	maxWorkers     = 4
	workersDivisor = 2
)

var (
	errSemaphoreIsNotInitialised = errors.New("semaphore is not initialised, default value is used")
)

var errNotPresentOrDisabled = errors.New("is not present or disabled")

// BoolVariation return the value of the flag in boolean.
// An error is return if you don't have init the library before calling the function.
// If the key does not exist we return the default value.
// Note: Use this function only if you are using multiple semaphore instances.
func (g *GoFeatureFlag) BoolVariation(
	flagKey string,
	ctx ffcontext.Context,
	defaultValue bool,
) (bool, error) {
	res, err := g.BoolVariationDetails(flagKey, ctx, defaultValue)

	return res.Value, err
}

// BoolVariationDetails return the details of the evaluation for boolean flag.
// An error is return if you don't have init the library before calling the function.
// If the key does not exist we return the default value.
// Note: Use this function only if you are using multiple semaphore instances.
func (g *GoFeatureFlag) BoolVariationDetails(
	flagKey string,
	ctx ffcontext.Context,
	defaultValue bool,
) (model.VariationResult[bool], error) {
	res, err := getVariation(g, flagKey, ctx, defaultValue, "bool")
	notifyVariation(g, flagKey, ctx, res)

	return res, err
}

// IntVariation return the value of the flag in int.
// An error is return if you don't have init the library before calling the function.
// If the key does not exist we return the default value.
// Note: Use this function only if you are using multiple semaphore instances.
func (g *GoFeatureFlag) IntVariation(
	flagKey string,
	ctx ffcontext.Context,
	defaultValue int,
) (int, error) {
	res, err := g.IntVariationDetails(flagKey, ctx, defaultValue)

	return res.Value, err
}

// IntVariationDetails return the details of the evaluation for int flag.
// An error is return if you don't have init the library before calling the function.
// If the key does not exist we return the default value.
// Note: Use this function only if you are using multiple semaphore instances.
func (g *GoFeatureFlag) IntVariationDetails(flagKey string, ctx ffcontext.Context, defaultValue int,
) (model.VariationResult[int], error) {
	res, err := getVariation(g, flagKey, ctx, defaultValue, "int")
	notifyVariation(g, flagKey, ctx, res)

	return res, err
}

// Float64Variation return the value of the flag in float64.
// An error is return if you don't have init the library before calling the function.
// If the key does not exist we return the default value.
// Note: Use this function only if you are using multiple semaphore instances.
func (g *GoFeatureFlag) Float64Variation(
	flagKey string,
	ctx ffcontext.Context,
	defaultValue float64,
) (float64, error) {
	res, err := g.Float64VariationDetails(flagKey, ctx, defaultValue)

	return res.Value, err
}

// Float64VariationDetails return the details of the evaluation for float64 flag.
// An error is return if you don't have init the library before calling the function.
// If the key does not exist we return the default value.
// Note: Use this function only if you are using multiple semaphore instances.
func (g *GoFeatureFlag) Float64VariationDetails(
	flagKey string,
	ctx ffcontext.Context,
	defaultValue float64,
) (model.VariationResult[float64], error) {
	res, err := getVariation(g, flagKey, ctx, defaultValue, "float64")
	notifyVariation(g, flagKey, ctx, res)

	return res, err
}

// StringVariation return the value of the flag in string.
// An error is return if you don't have init the library before calling the function.
// If the key does not exist we return the default value.
// Note: Use this function only if you are using multiple semaphore instances.
func (g *GoFeatureFlag) StringVariation(
	flagKey string,
	ctx ffcontext.Context,
	defaultValue string,
) (string, error) {
	res, err := g.StringVariationDetails(flagKey, ctx, defaultValue)

	return res.Value, err
}

// StringVariationDetails return the details of the evaluation for string flag.
// An error is return if you don't have init the library before calling the function.
// If the key does not exist we return the default value.
// Note: Use this function only if you are using multiple semaphore instances.
func (g *GoFeatureFlag) StringVariationDetails(
	flagKey string,
	ctx ffcontext.Context,
	defaultValue string,
) (model.VariationResult[string], error) {
	res, err := getVariation(g, flagKey, ctx, defaultValue, "string")
	notifyVariation(g, flagKey, ctx, res)

	return res, err
}

// JSONArrayVariation return the value of the flag in []interface{}.
// An error is return if you don't have init the library before calling the function.
// If the key does not exist we return the default value.
// Note: Use this function only if you are using multiple semaphore instances.
func (g *GoFeatureFlag) JSONArrayVariation(
	flagKey string, ctx ffcontext.Context, defaultValue []any,
) ([]any, error) {
	res, err := g.JSONArrayVariationDetails(flagKey, ctx, defaultValue)

	return res.Value, err
}

// JSONArrayVariationDetails return the details of the evaluation for []interface{} flag.
// An error is return if you don't have init the library before calling the function.
// If the key does not exist we return the default value.
// Note: Use this function only if you are using multiple semaphore instances.
func (g *GoFeatureFlag) JSONArrayVariationDetails(
	flagKey string,
	ctx ffcontext.Context,
	defaultValue []any,
) (model.VariationResult[[]any], error) {
	res, err := getVariation(g, flagKey, ctx, defaultValue, "[]interface{}")
	notifyVariation(g, flagKey, ctx, res)

	return res, err
}

// JSONVariation return the value of the flag in map[string]interface{}.
// An error is return if you don't have init the library before calling the function.
// If the key does not exist we return the default value.
// Note: Use this function only if you are using multiple semaphore instances.
func (g *GoFeatureFlag) JSONVariation(
	flagKey string, ctx ffcontext.Context, defaultValue map[string]any,
) (map[string]any, error) {
	res, err := g.JSONVariationDetails(flagKey, ctx, defaultValue)

	return res.Value, err
}

// JSONVariationDetails return the details of the evaluation for []interface{} flag.
// An error is return if you don't have init the library before calling the function.
// If the key does not exist we return the default value.
// Note: Use this function only if you are using multiple semaphore instances.
func (g *GoFeatureFlag) JSONVariationDetails(
	flagKey string,
	ctx ffcontext.Context,
	defaultValue map[string]any,
) (model.VariationResult[map[string]any], error) {
	res, err := getVariation(g, flagKey, ctx, defaultValue, "bool")
	notifyVariation(g, flagKey, ctx, res)

	return res, err
}

// RawVariation return the raw value of the flag (without any types).
// This raw result is mostly used by software built on top of semaphore such as
// semaphore relay proxy.
// If you are using directly the library you should avoid calling this function.
// Note: Use this function only if you are using multiple semaphore instances.
func (g *GoFeatureFlag) RawVariation(
	flagKey string,
	ctx ffcontext.Context,
	sdkDefaultValue any,
) (model.RawVarResult, error) {
	res, err := getVariation(g, flagKey, ctx, sdkDefaultValue, "interface{}")
	notifyVariation(g, flagKey, ctx, res)

	return model.RawVarResult(res), err
}

// getFlagFromCache try to get the flag from the cache.
// It returns an error if the cache is not init or if the flag is not present or disabled.
func (g *GoFeatureFlag) getFlagFromCache(flagKey string) (flag.Flag, error) {
	f, err := g.retrieverManager.GetFlag(flagKey)
	if err != nil {
		return f, fmt.Errorf("flag %v %w", flagKey, errNotPresentOrDisabled)
	}

	return f, nil
}

// CollectEventData is collecting events and sending them to the data exporter to be stored.
func (g *GoFeatureFlag) CollectEventData(event exporter.FeatureEvent) {
	if g != nil && g.featureEventDataExporter != nil {
		// Add event in the exporter
		g.featureEventDataExporter.AddEvent(event)
	}
}

// queueEvents adds the events in the exporter without waiting for a flush (see exporter.AsyncManager).
func (g *GoFeatureFlag) queueEvents(events []exporter.FeatureEvent) {
	if g.featureEventDataExporter == nil {
		return
	}
	if m, ok := g.featureEventDataExporter.(exporter.AsyncManager[exporter.FeatureEvent]); ok {
		m.QueueEvents(events)

		return
	}
	for _, event := range events {
		g.featureEventDataExporter.AddEvent(event)
	}
}

// CollectTrackingEventData is collecting tracking events and sending them to the data exporter to be stored.
func (g *GoFeatureFlag) CollectTrackingEventData(event exporter.TrackingEvent) {
	if g != nil && g.trackingEventDataExporter != nil {
		// Add event in the exporter
		g.trackingEventDataExporter.AddEvent(event)
	}
}

// notifyVariation is logging the evaluation result for a flag
// if no logger is provided in the configuration we are not logging anything.
func notifyVariation[T model.JSONType](
	g *GoFeatureFlag,
	flagKey string,
	ctx ffcontext.Context,
	result model.VariationResult[T],
) {
	if result.TrackEvents && g.featureEventDataExporter != nil {
		// the event describes the context as it was evaluated: with the configured enrichment
		// (only what the event reads is worth merging: the exporter metadata and the anonymous status).
		if enrichment := g.evaluationEnrichment(); changesEventFields(enrichment) {
			ctx = flag.EnrichContext(ctx, enrichment)
		}
		event := exporter.NewFeatureEvent(
			ctx,
			flagKey,
			result.Value,
			result.VariationType,
			result.Failed,
			result.Version,
			"SERVER",
			ctx.ExtractGOFFProtectedFields().ExporterMetadata,
		)
		g.enqueueEvent(event)
	}
}

// getVariation is the internal generic func that handle the logic of a variation the result will always
// contain a valid model.VariationResult
//
//nolint:funlen
func getVariation[T model.JSONType](
	g *GoFeatureFlag,
	flagKey string,
	evaluationCtx ffcontext.Context,
	sdkDefaultValue T,
	expectedType string,
) (model.VariationResult[T], error) {
	if g == nil {
		return model.VariationResult[T]{
			Value:         sdkDefaultValue,
			VariationType: flag.VariationSDKDefault,
			Failed:        true,
			Reason:        flag.ReasonError,
			ErrorCode:     flag.ErrorCodeProviderNotReady,
			Cacheable:     false,
		}, errSemaphoreIsNotInitialised
	}
	if g.config.Offline {
		return model.VariationResult[T]{
			Value:         sdkDefaultValue,
			VariationType: flag.VariationSDKDefault,
			Failed:        false,
			Reason:        flag.ReasonOffline,
			Cacheable:     false,
		}, nil
	}

	f, err := g.getFlagFromCache(flagKey)
	if err != nil {
		varResult := model.VariationResult[T]{
			Value:         sdkDefaultValue,
			VariationType: flag.VariationSDKDefault,
			ErrorCode:     flag.ErrorCodeFlagNotFound,
			Failed:        true,
			Reason:        flag.ReasonError,
			Cacheable:     false,
		}
		if f != nil {
			varResult.TrackEvents = f.IsTrackEvents()
			varResult.Version = f.GetVersion()
			varResult.Metadata = f.GetMetadata()
		}

		return varResult, err
	}

	flagCtx := flag.Context{
		DefaultSdkValue:             sdkDefaultValue,
		EvaluationContextEnrichment: g.evaluationEnrichment(),
		Service:                     g.evaluationService,
	}

	return evaluation.Evaluate[T](f, flagKey, evaluationCtx, flagCtx, expectedType, sdkDefaultValue)
}

// startEventWorker starts the goroutines that move the evaluation events from the queue to the
// exporters. It does nothing when there is no exporter for the evaluation events.
func (g *GoFeatureFlag) startEventWorker() {
	if g.featureEventDataExporter == nil {
		return
	}
	size := g.config.EventQueueSize
	if size <= 0 {
		size = DefaultEventQueueSize
	}
	g.eventQueue = make(chan exporter.FeatureEvent, size)
	g.eventWorkerStop = make(chan struct{})
	g.dropSignal = make(chan struct{}, 1)
	go g.reportDrops()
	// A single worker adds about 600k events/s to the exporters (one mutex and a copy of the event
	// per event): a few workers keep up with an evaluation rate of millions per second.
	workers := max(1, min(maxWorkers, runtime.GOMAXPROCS(0)/workersDivisor))
	for range workers {
		g.evalExporterWg.Add(1)
		go g.runEventWorker()
	}
}

// eventBatchSize is the maximum number of events a worker hands to the exporter at once.
const eventBatchSize = 256

// runEventWorker moves the events of the queue to the exporters until the client is closed,
// then drains what is left in the queue. The events that are already waiting are handed over
// together, which takes the lock of the exporter's store once per batch instead of once per event.
func (g *GoFeatureFlag) runEventWorker() {
	defer g.evalExporterWg.Done()
	batch := make([]exporter.FeatureEvent, 0, eventBatchSize)
	drain := func() {
		for len(batch) < eventBatchSize {
			select {
			case event := <-g.eventQueue:
				batch = append(batch, event)
			default:
				return
			}
		}
	}
	for {
		select {
		case event := <-g.eventQueue:
			batch = append(batch[:0], event)
			drain()
			g.queueEvents(batch)
		case <-g.eventWorkerStop:
			for {
				batch = batch[:0]
				drain()
				if len(batch) == 0 {
					return
				}
				g.queueEvents(batch)
			}
		}
	}
}

// enqueueEvent hands the event to the worker without ever blocking the evaluation.
// If the queue is full the event is dropped and counted.
func (g *GoFeatureFlag) enqueueEvent(event exporter.FeatureEvent) {
	if g.eventQueue == nil {
		// no worker (struct not built by New): add the event directly.
		g.CollectEventData(event)

		return
	}
	g.eventAdmission.RLock()
	if g.closing {
		// the client is closing (an evaluation that was running when its flag set was removed, for
		// example): the workers are draining or gone, hand the event to the exporters directly so
		// that their final flush exports it.
		g.eventAdmission.RUnlock()
		g.CollectEventData(event)

		return
	}
	select {
	case g.eventQueue <- event:
		g.eventAdmission.RUnlock()
	default:
		g.eventAdmission.RUnlock()
		g.droppedEvents.Add(1)
		// never log here: an evaluation must not wait for the log sink (see reportDrops).
		select {
		case g.dropSignal <- struct{}{}:
		default: // a warning is already pending
		}
	}
}

// dropLogInterval is the minimum delay between two warnings about dropped evaluation events.
const dropLogInterval = 10 * time.Second

// reportDrops writes (at most once every dropLogInterval) the warning about the events dropped
// because the queue was full. It runs in its own goroutine, which is not waited by Close, so that
// a slow or blocked log sink never delays an evaluation, a worker or the shutdown.
func (g *GoFeatureFlag) reportDrops() {
	for {
		select {
		case <-g.dropSignal:
		case <-g.eventWorkerStop:
			return
		}
		g.config.internalLogger.Error(
			"the exporters are not keeping up, evaluation events are dropped (drop-newest)",
			slog.Int64("totalDroppedEvents", g.droppedEvents.Load()))
		timer := time.NewTimer(dropLogInterval)
		select {
		case <-timer.C:
		case <-g.eventWorkerStop:
			timer.Stop()

			return
		}
	}
}

// DroppedEvents returns the number of events that have been dropped since the start because the
// exporters were not able to keep up (queue between the evaluations and the exporters full, or
// exporter queue at its MaxQueuedEvents limit). It is 0 in a healthy setup.
func (g *GoFeatureFlag) DroppedEvents() int64 {
	if g == nil {
		return 0
	}
	total := g.droppedEvents.Load()
	if c, ok := g.featureEventDataExporter.(exporter.DroppedEventsCounter); ok {
		total += c.DroppedEvents()
	}
	if c, ok := g.trackingEventDataExporter.(exporter.DroppedEventsCounter); ok {
		total += c.DroppedEvents()
	}

	return total
}

// evaluationEnrichment returns the evaluation context enrichment of the configuration with the
// environment ("env") merged in. It is built once and must be treated as read-only: the
// evaluations are concurrent.
func (g *GoFeatureFlag) evaluationEnrichment() map[string]any {
	g.enrichmentOnce.Do(func() {
		enrichment := maps.Clone(g.config.EvaluationContextEnrichment)
		if g.config.Environment != "" {
			if enrichment == nil {
				enrichment = make(map[string]any, 1)
			}
			enrichment["env"] = g.config.Environment
		}
		g.enrichment = enrichment
	})

	return g.enrichment
}

// changesEventFields tells if the enrichment sets an attribute that an evaluation event reads.
func changesEventFields(enrichment map[string]any) bool {
	protected := ffcontext.HasReservedKey(enrichment)
	_, anonymous := enrichment["anonymous"]

	return protected || anonymous
}
