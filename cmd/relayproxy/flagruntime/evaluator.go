package flagruntime

import (
	"errors"
	"fmt"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/flagstate"
	"github.com/blackbirdworks/semaphore/modules/core/evaluation"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/model"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// ErrFlagNotPresent is wrapped by the error of an evaluation of a flag that the flag set does not hold. Its
// text is the one of the version 1 client ("flag <key> is not present or disabled").
var ErrFlagNotPresent = errors.New("is not present or disabled")

// envKey is the evaluation context attribute that carries the environment.
const envKey = "env"

// EvaluatorConfig is the configuration of an [Evaluator]. It is read once, by [NewEvaluator]; the Evaluator keeps
// no reference to the maps.
type EvaluatorConfig struct {
	// EvaluationContextEnrichment is merged into the context of every evaluation of every flag set that has no
	// entry of its own in FlagSetEnrichment (the version 1 evaluationContextEnrichment).
	EvaluationContextEnrichment map[string]any
	// FlagSetEnrichment replaces EvaluationContextEnrichment for the flag sets it names (by flag set name).
	FlagSetEnrichment map[string]map[string]any
	// TrackEvent receives the event of an evaluation that must be tracked: the evaluation says trackEvents (the
	// setting of the flag) and the hook is set. A nil hook costs nothing. The hook runs on the goroutine of the
	// evaluation and must not block; the events package of the relay proxy plugs its queue in here.
	//
	// The event is READ-ONLY for the hook and for whatever it hands the event to (the exporters): its Value is the
	// value of the flag, shared with the flag set and with every other evaluation, and its Metadata is the
	// exporter-metadata map of the request context, as in version 1. The hook must encode or copy them, never
	// change them; [flagdef.CloneValue] and [flagdef.CloneMetadata] make a copy.
	TrackEvent func(*exporter.FeatureEvent)
	// Environment is added to the enrichment under "env" when it is not empty.
	Environment string
	// DisableSemaphoreMetadata keeps the Semaphore metadata key (semaphore_cacheable) out of the response metadata
	// of [Evaluator.ResponseMetadata]; the legacy key is always sent (the disableSemaphoreMetadata setting).
	DisableSemaphoreMetadata bool
}

// enrichment is the enrichment of one flag set, built once: values is what the evaluations merge (with the
// environment), configured is what the flag configuration endpoint shows (without it).
type enrichment struct {
	values     map[string]any
	configured map[string]any
	// eventFields tells that the enrichment sets an attribute that an evaluation event reads.
	eventFields bool
}

// newEnrichment copies the configured enrichment deeply: the caller keeps its map, and nothing it changes later
// reaches the evaluations.
func newEnrichment(configured map[string]any, environment string) *enrichment {
	values := flagdef.CloneMetadata(configured)
	if environment != "" {
		if values == nil {
			values = make(map[string]any, 1)
		}

		values[envKey] = environment
	}

	_, anonymous := values["anonymous"]

	return &enrichment{
		values:      values,
		configured:  flagdef.CloneMetadata(configured),
		eventFields: ffcontext.HasReservedKey(values) || anonymous,
	}
}

// Evaluator evaluates the flags of a [FlagSet]. It is the replacement of the evaluation methods of the version 1
// client, with the same results, and it is created once per process: it owns the [flagdef.Service] of the
// evaluations (the caches of parsed queries and scheduled rollouts), which is the service the [Engine] validates
// with, so a flag is never parsed twice. There is no client, cache, notifier or goroutine per flag set.
//
// An Evaluator is immutable and safe for concurrent use. It lives outside [FlagSet] because the flag sets are
// shared between generations while the service and the enrichment belong to the process.
//
// Ownership of what it returns (the read-only contract of the version 1 client):
//   - The flags, and everything inside them, are shared by every flag set that lists the same object and by every
//     evaluation. The value of an evaluation ([Evaluator.RawVariation], the values of [Evaluator.AllFlagsState] and
//     [Evaluator.GetFlagStates], the value of a tracked event) and the containers that the metadata holds (nested
//     objects and arrays) are those of the flag: they are READ-ONLY. Encode them, do not change them. A caller that
//     must change one takes a copy with [flagdef.CloneValue] or [flagdef.CloneMetadata].
//   - The metadata map itself, and the result structs, are new for every evaluation (the engine builds them): the
//     caller owns them and may add keys, as [Evaluator.ResponseMetadata] does.
//   - [Evaluator.JSONVariationDetails] and [Evaluator.JSONArrayVariationDetails] return a copy of the value: the
//     caller owns it.
//   - The default value that the caller passed in is returned as given.
//
// A handler must therefore never write into a result; the switch to config v2 adds a guard for it.
type Evaluator struct {
	svc    *flagdef.Service
	def    *enrichment
	perSet map[string]*enrichment
	track  func(*exporter.FeatureEvent)
	// missing is the version of an evaluation of a flag that is not there: version 1 answers with what an empty flag
	// says (it tracks and has a default version), and so does the runtime.
	missing missingFlag
	// legacyMetadataOnly drops the Semaphore cacheable key from the response metadata.
	legacyMetadataOnly bool
}

// missingFlag holds what an empty flag reports; it is read once at construction.
type missingFlag struct {
	version     string
	trackEvents bool
}

// NewEvaluator returns an Evaluator. svc is the service that the Engine of the same runtime is built with.
func NewEvaluator(svc *flagdef.Service, cfg *EvaluatorConfig) *Evaluator {
	if cfg == nil {
		cfg = &EvaluatorConfig{}
	}

	eval := &Evaluator{
		svc:   svc,
		def:   newEnrichment(cfg.EvaluationContextEnrichment, cfg.Environment),
		track: cfg.TrackEvent,

		legacyMetadataOnly: cfg.DisableSemaphoreMetadata,
	}

	var empty flagdef.InternalFlag

	eval.missing = missingFlag{version: empty.GetVersion(), trackEvents: empty.IsTrackEvents()}

	if len(cfg.FlagSetEnrichment) > 0 {
		eval.perSet = make(map[string]*enrichment, len(cfg.FlagSetEnrichment))
		for name, values := range cfg.FlagSetEnrichment {
			eval.perSet[name] = newEnrichment(values, cfg.Environment)
		}
	}

	return eval
}

// The metadata keys that tell a provider that a value can be cached: the Semaphore name and the legacy one, which
// the upstream providers read.
const (
	// MetadataCacheable is the Semaphore key.
	MetadataCacheable = "semaphore_cacheable"
	// MetadataSegmentStatus is the Semaphore key of the health of the segment lookups of an evaluation: STALE,
	// NOT_CONFIGURED or STORE_ERROR, the worst of the segments of the evaluation (the names of LaunchDarkly's
	// bigSegmentsStatus). A healthy evaluation, and one that looked at no segment, do not carry it.
	MetadataSegmentStatus = "semaphore_segmentStatus"
	// LegacyMetadataCacheable is the legacy key, always sent.
	LegacyMetadataCacheable = "gofeatureflag_cacheable"
)

// ResponseMetadata returns the metadata of the OFREP response of an evaluation: the metadata of the result plus, for
// a cacheable result, the legacy key and (unless DisableSemaphoreMetadata is set) the Semaphore key, as the version 1
// handler adds them, and (unless DisableSemaphoreMetadata is set) [MetadataSegmentStatus] for an evaluation that looked
// at a segment. The metadata map of a result is owned by the caller (see [Evaluator]), so the keys are added
// to it without a copy and the flag is never touched; a result without metadata gets a new map only when it is
// cacheable.
func (e *Evaluator) ResponseMetadata(res *model.RawVarResult) map[string]any {
	metadata := res.Metadata
	withStatus := res.SegmentStatus.Reported() && !e.legacyMetadataOnly

	if !res.Cacheable && !withStatus {
		return metadata
	}

	if metadata == nil {
		metadata = make(map[string]any, 3) //nolint:mnd // the cacheable keys and the segment status
	}

	if withStatus {
		metadata[MetadataSegmentStatus] = res.SegmentStatus.String()
	}

	if !res.Cacheable {
		return metadata
	}

	if !e.legacyMetadataOnly {
		metadata[MetadataCacheable] = true
	}

	metadata[LegacyMetadataCacheable] = true

	return metadata
}

// EvaluationContextEnrichment returns a deep copy of the configured enrichment of the flag set, without the
// environment: the "evaluationContextEnrichment" of the flag configuration endpoint. The caller owns it.
func (e *Evaluator) EvaluationContextEnrichment(set *FlagSet) map[string]any {
	return flagdef.CloneMetadata(e.enrichmentFor(set).configured)
}

// RawVariation is the evaluation of a flag without a type, as the engine client's RawVariation: the value is
// whatever the flag holds. An object or array value, and the nested containers of the metadata, are shared with the
// flag and READ-ONLY (see [Evaluator]); the metadata map itself belongs to the caller.
func (e *Evaluator) RawVariation(
	set *FlagSet, flagKey string, evalCtx ffcontext.Context, sdkDefaultValue any,
) (model.RawVarResult, error) {
	res, err := variation(e, set, flagKey, evalCtx, sdkDefaultValue, typeAny)
	notify(e, set, flagKey, evalCtx, &res, isDefaultResult(res.Reason))

	return model.RawVarResult(res), err
}

// BoolVariationDetails evaluates a boolean flag.
func (e *Evaluator) BoolVariationDetails(
	set *FlagSet, flagKey string, evalCtx ffcontext.Context, defaultValue bool,
) (model.VariationResult[bool], error) {
	res, err := variation(e, set, flagKey, evalCtx, defaultValue, typeBool)
	notify(e, set, flagKey, evalCtx, &res, false)

	return res, err
}

// IntVariationDetails evaluates an integer flag.
func (e *Evaluator) IntVariationDetails(
	set *FlagSet, flagKey string, evalCtx ffcontext.Context, defaultValue int,
) (model.VariationResult[int], error) {
	res, err := variation(e, set, flagKey, evalCtx, defaultValue, typeInt)
	notify(e, set, flagKey, evalCtx, &res, false)

	return res, err
}

// Float64VariationDetails evaluates a number flag.
func (e *Evaluator) Float64VariationDetails(
	set *FlagSet, flagKey string, evalCtx ffcontext.Context, defaultValue float64,
) (model.VariationResult[float64], error) {
	res, err := variation(e, set, flagKey, evalCtx, defaultValue, typeFloat64)
	notify(e, set, flagKey, evalCtx, &res, false)

	return res, err
}

// StringVariationDetails evaluates a string flag.
func (e *Evaluator) StringVariationDetails(
	set *FlagSet, flagKey string, evalCtx ffcontext.Context, defaultValue string,
) (model.VariationResult[string], error) {
	res, err := variation(e, set, flagKey, evalCtx, defaultValue, typeString)
	notify(e, set, flagKey, evalCtx, &res, false)

	return res, err
}

// JSONArrayVariationDetails evaluates an array flag. The value is a copy that the caller owns; the nested containers
// of the metadata are shared and READ-ONLY (see [Evaluator]).
func (e *Evaluator) JSONArrayVariationDetails(
	set *FlagSet, flagKey string, evalCtx ffcontext.Context, defaultValue []any,
) (model.VariationResult[[]any], error) {
	res, err := variation(e, set, flagKey, evalCtx, defaultValue, typeArray)
	shared := sharesFlagValue(set, flagKey, res.Reason)

	notify(e, set, flagKey, evalCtx, &res, !shared)

	if res.Value != nil && shared {
		res.Value, _ = flagdef.CloneValue(res.Value).([]any)
	}

	return res, err
}

// JSONVariationDetails evaluates an object flag. The value is a copy that the caller owns; the nested containers of
// the metadata are shared and READ-ONLY (see [Evaluator]).
func (e *Evaluator) JSONVariationDetails(
	set *FlagSet, flagKey string, evalCtx ffcontext.Context, defaultValue map[string]any,
) (model.VariationResult[map[string]any], error) {
	res, err := variation(e, set, flagKey, evalCtx, defaultValue, typeObject)
	shared := sharesFlagValue(set, flagKey, res.Reason)

	notify(e, set, flagKey, evalCtx, &res, !shared)

	if res.Value != nil && shared {
		res.Value, _ = flagdef.CloneValue(res.Value).(map[string]any)
	}

	return res, err
}

// The expected types of a variation; only "int" changes the evaluation (a number is converted to an integer).
const (
	typeAny     = "interface{}"
	typeBool    = "bool"
	typeInt     = "int"
	typeFloat64 = "float64"
	typeString  = "string"
	typeArray   = "[]interface{}"
	typeObject  = "map[string]interface{}"
)

// variation evaluates one flag of a flag set. The result always holds a valid value; the error is set for a
// flag that is not there and for a value of another type than the one asked.
func variation[T model.JSONType](
	eval *Evaluator, set *FlagSet, flagKey string, evalCtx ffcontext.Context, sdkDefaultValue T, expectedType string,
) (model.VariationResult[T], error) {
	flag, ok := set.Flag(flagKey)
	if !ok {
		return model.VariationResult[T]{
			Value:         sdkDefaultValue,
			VariationType: flagdef.VariationSDKDefault,
			ErrorCode:     flagdef.ErrorCodeFlagNotFound,
			ErrorDetails:  "",
			Failed:        true,
			Reason:        flagdef.ReasonError,
			TrackEvents:   eval.missing.trackEvents,
			Version:       eval.missing.version,
			Metadata:      nil,
			Cacheable:     false,
			SegmentStatus: segment.StatusNone,
		}, fmt.Errorf("flag %v %w", flagKey, ErrFlagNotPresent)
	}

	flagCtx := flagdef.Context{
		DefaultSdkValue:             sdkDefaultValue,
		EvaluationContextEnrichment: eval.enrichmentFor(set).values,
		Service:                     eval.svc,
		SegmentCatalog:              set.SegmentCatalogFor(flagKey),
	}

	res, err := evaluation.Evaluate[T](
		flag,
		flagKey,
		evalCtx,
		flagCtx,
		expectedType,
		sdkDefaultValue,
	)

	// the members of a dynamic segment live in a store and change without a new flag: a client must not hold the
	// answer, whether or not this evaluation reached the segment
	if set.usesDynamicSegment(flagKey) {
		res.Cacheable = false
		dropCacheableKeys(res.Metadata)
	}

	return res, err
}

// dropCacheableKeys removes the cacheable keys that a flag wrote in its own metadata: the metadata map of a result is
// the caller's own, and a flag that uses a dynamic segment must not tell a client that its value can be held.
func dropCacheableKeys(metadata map[string]any) {
	delete(metadata, MetadataCacheable)
	delete(metadata, LegacyMetadataCacheable)
}

// isDefaultResult tells whether a result with this reason carries the default value of the caller (a flag that is not
// there, a disabled flag, an error of the evaluation, a value of another type than the one asked) and not a value of
// the flag.
func isDefaultResult(reason flagdef.ResolutionReason) bool {
	return reason == flagdef.ReasonError || reason == flagdef.ReasonDisabled
}

// sharesFlagValue tells whether the value of a result is still the value stored in the flag, so that a typed call
// that hands out an owned copy must copy it. It is not when the result carries the default value of the caller (an
// error or a disabled flag: the caller owns it and gets it as given), and not when the flag has scheduled steps:
// the engine evaluates those on a copy of the flag and already copies the value, once.
func sharesFlagValue(set *FlagSet, flagKey string, reason flagdef.ResolutionReason) bool {
	if isDefaultResult(reason) {
		return false
	}

	flag, ok := set.Flag(flagKey)

	return ok && flag.Scheduled == nil
}

// notify hands the event of an evaluation to the hook when the evaluation must be tracked. It is the version 1
// condition: the evaluation says trackEvents (the flag setting) and there is somewhere to send the event. A nil
// context is the empty one, as it is for the evaluation itself.
//
// The rule for the value of the event, the same for every entry point: the exporter keeps the event, so its value is
// never a value that the caller can change. A value of the flag is shared and read only, the caller does not change
// it (and a typed call hands out a copy of it): the event keeps the flag's value. A value that the caller owns is
// snapshotted for the event when own is set: its own default (a flag that is not there, a disabled flag, an error, a
// value of another type), and the copy that the engine made for a scheduled flag when a typed call hands it out. The
// result keeps the value as it is. The all-flags state does not track.
func notify[T model.JSONType](
	eval *Evaluator, set *FlagSet, flagKey string, evalCtx ffcontext.Context, res *model.VariationResult[T], own bool,
) {
	if eval.track == nil || !res.TrackEvents {
		return
	}

	if evalCtx == nil {
		evalCtx = ffcontext.EvaluationContext{}
	}

	// the event describes the context as it was evaluated: with the enrichment, when the event reads it
	if enr := eval.enrichmentFor(set); enr.eventFields {
		evalCtx = flagdef.NewEnrichedContext(evalCtx, enr.values)
	}

	var value any = res.Value
	if own {
		value = snapshotValue(value)
	}

	eval.track(exporter.NewFeatureEvent(
		evalCtx, flagKey, value, res.VariationType, res.Failed, res.Version, "SERVER",
		evalCtx.ExtractGOFFProtectedFields().ExporterMetadata,
	))
}

// snapshotValue copies the objects and arrays of a value, deeply, at every level. The containers of a variation are
// map[string]any, map[any]any and []any: a nil one stays a nil one of the same type (a typed nil map is not a nil
// interface to an exporter, and a null is not an empty container), and a container that is not nil is copied. Any other
// value is immutable or not a container of a variation (scalars, strings, a []string, a struct) and is returned as it
// is, by reference.
func snapshotValue(val any) any {
	switch typed := val.(type) {
	case map[string]any:
		return snapshotMap(typed)
	case map[any]any:
		return snapshotAnyMap(typed)
	case []any:
		return snapshotSlice(typed)
	default:
		return val
	}
}

func snapshotMap(src map[string]any) map[string]any {
	var out map[string]any
	if src == nil {
		return out
	}

	out = make(map[string]any, len(src))
	for key, elem := range src {
		out[key] = snapshotValue(elem)
	}

	return out
}

func snapshotAnyMap(src map[any]any) map[any]any {
	var out map[any]any
	if src == nil {
		return out
	}

	out = make(map[any]any, len(src))
	for key, elem := range src {
		out[key] = snapshotValue(elem)
	}

	return out
}

func snapshotSlice(src []any) []any {
	var out []any
	if src == nil {
		return out
	}

	out = make([]any, len(src))
	for idx, elem := range src {
		out[idx] = snapshotValue(elem)
	}

	return out
}

// AllFlagsState evaluates every flag of the flag set for a context. A nil flag set (not available) answers like an
// empty one: valid, no flags. The values and the nested metadata containers of the states are shared with the flags
// and READ-ONLY (see [Evaluator]).
func (e *Evaluator) AllFlagsState(set *FlagSet, evalCtx ffcontext.Context) flagstate.AllFlags {
	return e.GetFlagStates(set, evalCtx, nil)
}

// GetFlagStates evaluates the flags named in flagsToEvaluate, or every flag when the list is empty. A name that
// the flag set does not hold is left out. The values and the nested metadata containers of the states are shared
// with the flags and READ-ONLY (see [Evaluator]).
func (e *Evaluator) GetFlagStates(
	set *FlagSet, evalCtx ffcontext.Context, flagsToEvaluate []string,
) flagstate.AllFlags {
	flagCtx := flagdef.Context{
		EvaluationContextEnrichment: e.enrichmentFor(set).values,
		DefaultSdkValue:             nil,
		Service:                     e.svc,
	}

	states := flagstate.NewAllFlags()

	if len(flagsToEvaluate) != 0 {
		for _, key := range flagsToEvaluate {
			flag, ok := set.Flag(key)
			if !ok {
				continue
			}

			flagCtx.SegmentCatalog = set.SegmentCatalogFor(key)
			addState(&states, key, evalCtx, flagCtx, flag, set.usesDynamicSegment(key))
		}

		return states
	}

	if set == nil {
		return states
	}

	for key, flag := range set.Flags {
		flagCtx.SegmentCatalog = set.SegmentCatalogFor(key)
		addState(&states, key, evalCtx, flagCtx, flag, set.usesDynamicSegment(key))
	}

	return states
}

func addState(
	states *flagstate.AllFlags, key string, evalCtx ffcontext.Context, flagCtx flagdef.Context,
	flag *flagdef.InternalFlag, dynamic bool,
) {
	state := flagstate.FromFlagEvaluation(key, evalCtx, flagCtx, flag)
	if dynamic {
		dropCacheableKeys(state.Metadata)
	}

	states.AddFlag(key, &state)
}

// enrichmentFor returns the enrichment of a flag set. It does not allocate.
func (e *Evaluator) enrichmentFor(set *FlagSet) *enrichment {
	if e.perSet != nil && set != nil {
		if own, ok := e.perSet[set.Name]; ok {
			return own
		}
	}

	return e.def
}
