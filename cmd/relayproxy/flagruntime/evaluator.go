package flagruntime

import (
	"errors"
	"fmt"
	"maps"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/flagstate"
	"github.com/blackbirdworks/semaphore/modules/core/evaluation"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/model"
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

func newEnrichment(configured map[string]any, environment string) *enrichment {
	values := maps.Clone(configured)
	if environment != "" {
		if values == nil {
			values = make(map[string]any, 1)
		}

		values[envKey] = environment
	}

	_, anonymous := values["anonymous"]

	return &enrichment{
		values:      values,
		configured:  maps.Clone(configured),
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
// Ownership of what it returns: a variation value that is a map or a slice, and every map of the metadata, are
// copies that the caller may change; the flags themselves are shared and read-only. The value that is the
// default of the SDK is returned as given.
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
	// LegacyMetadataCacheable is the legacy key, always sent.
	LegacyMetadataCacheable = "gofeatureflag_cacheable"
)

// ResponseMetadata returns the metadata of the OFREP response of an evaluation: the metadata of the result plus, for
// a cacheable result, the legacy key and (unless DisableSemaphoreMetadata is set) the Semaphore key, as the version 1
// handler adds them. The metadata of a result is owned by the caller (see [Evaluator]), so the keys are added to it
// without a copy; a result without metadata gets a new map only when it is cacheable.
func (e *Evaluator) ResponseMetadata(res *model.RawVarResult) map[string]any {
	metadata := res.Metadata
	if !res.Cacheable {
		return metadata
	}

	if metadata == nil {
		metadata = make(map[string]any, 2) //nolint:mnd // the two cacheable keys
	}

	if !e.legacyMetadataOnly {
		metadata[MetadataCacheable] = true
	}

	metadata[LegacyMetadataCacheable] = true

	return metadata
}

// EvaluationContextEnrichment returns a copy of the configured enrichment of the flag set, without the
// environment: the "evaluationContextEnrichment" of the flag configuration endpoint.
func (e *Evaluator) EvaluationContextEnrichment(set *FlagSet) map[string]any {
	return maps.Clone(e.enrichmentFor(set).configured)
}

// RawVariation is the evaluation of a flag without a type, as the engine client's RawVariation: the value is
// whatever the flag holds. An object or array value is a copy.
func (e *Evaluator) RawVariation(
	set *FlagSet, flagKey string, evalCtx ffcontext.Context, sdkDefaultValue any,
) (model.RawVarResult, error) {
	res, err := variation(e, set, flagKey, evalCtx, sdkDefaultValue, typeAny)
	notify(e, set, flagKey, evalCtx, &res)

	if holdsFlagValue(res.Reason) {
		res.Value = flagdef.CloneValue(res.Value)
	}

	return model.RawVarResult(res), err
}

// BoolVariationDetails evaluates a boolean flag.
func (e *Evaluator) BoolVariationDetails(
	set *FlagSet, flagKey string, evalCtx ffcontext.Context, defaultValue bool,
) (model.VariationResult[bool], error) {
	res, err := variation(e, set, flagKey, evalCtx, defaultValue, typeBool)
	notify(e, set, flagKey, evalCtx, &res)

	return res, err
}

// IntVariationDetails evaluates an integer flag.
func (e *Evaluator) IntVariationDetails(
	set *FlagSet, flagKey string, evalCtx ffcontext.Context, defaultValue int,
) (model.VariationResult[int], error) {
	res, err := variation(e, set, flagKey, evalCtx, defaultValue, typeInt)
	notify(e, set, flagKey, evalCtx, &res)

	return res, err
}

// Float64VariationDetails evaluates a number flag.
func (e *Evaluator) Float64VariationDetails(
	set *FlagSet, flagKey string, evalCtx ffcontext.Context, defaultValue float64,
) (model.VariationResult[float64], error) {
	res, err := variation(e, set, flagKey, evalCtx, defaultValue, typeFloat64)
	notify(e, set, flagKey, evalCtx, &res)

	return res, err
}

// StringVariationDetails evaluates a string flag.
func (e *Evaluator) StringVariationDetails(
	set *FlagSet, flagKey string, evalCtx ffcontext.Context, defaultValue string,
) (model.VariationResult[string], error) {
	res, err := variation(e, set, flagKey, evalCtx, defaultValue, typeString)
	notify(e, set, flagKey, evalCtx, &res)

	return res, err
}

// JSONArrayVariationDetails evaluates an array flag. The value is a copy.
func (e *Evaluator) JSONArrayVariationDetails(
	set *FlagSet, flagKey string, evalCtx ffcontext.Context, defaultValue []any,
) (model.VariationResult[[]any], error) {
	res, err := variation(e, set, flagKey, evalCtx, defaultValue, typeArray)
	notify(e, set, flagKey, evalCtx, &res)

	if res.Value != nil && holdsFlagValue(res.Reason) {
		res.Value, _ = flagdef.CloneValue(res.Value).([]any)
	}

	return res, err
}

// JSONVariationDetails evaluates an object flag. The value is a copy.
func (e *Evaluator) JSONVariationDetails(
	set *FlagSet, flagKey string, evalCtx ffcontext.Context, defaultValue map[string]any,
) (model.VariationResult[map[string]any], error) {
	res, err := variation(e, set, flagKey, evalCtx, defaultValue, typeObject)
	notify(e, set, flagKey, evalCtx, &res)

	if res.Value != nil && holdsFlagValue(res.Reason) {
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
		}, fmt.Errorf("flag %v %w", flagKey, ErrFlagNotPresent)
	}

	flagCtx := flagdef.Context{
		DefaultSdkValue:             sdkDefaultValue,
		EvaluationContextEnrichment: eval.enrichmentFor(set).values,
		Service:                     eval.svc,
	}

	res, err := evaluation.Evaluate[T](flag, flagKey, evalCtx, flagCtx, expectedType, sdkDefaultValue)
	res.Metadata = ownMetadata(res.Metadata)

	return res, err
}

// holdsFlagValue tells whether the value of a result with this reason comes from the flag. A result that is an
// error or a disabled flag carries the default value of the caller, which the caller owns and which is returned as
// given.
func holdsFlagValue(reason flagdef.ResolutionReason) bool {
	return reason != flagdef.ReasonError && reason != flagdef.ReasonDisabled
}

// ownMetadata makes the metadata of an evaluation safe to hand out. The map itself was made for the evaluation
// (the flag's own is never returned), but its values can still be the containers of the flag's metadata: those are
// replaced by copies, in place.
func ownMetadata(metadata map[string]any) map[string]any {
	for key, val := range metadata {
		switch val.(type) {
		case map[string]any, map[any]any, []any:
			metadata[key] = flagdef.CloneValue(val)
		}
	}

	return metadata
}

// notify hands the event of an evaluation to the hook when the evaluation must be tracked. It is the version 1
// condition: the evaluation says trackEvents (the flag setting) and there is somewhere to send the event.
func notify[T model.JSONType](
	eval *Evaluator, set *FlagSet, flagKey string, evalCtx ffcontext.Context, res *model.VariationResult[T],
) {
	if eval.track == nil || !res.TrackEvents {
		return
	}

	// the event describes the context as it was evaluated: with the enrichment, when the event reads it
	if enr := eval.enrichmentFor(set); enr.eventFields {
		evalCtx = flagdef.NewEnrichedContext(evalCtx, enr.values)
	}

	eval.track(exporter.NewFeatureEvent(
		evalCtx, flagKey, res.Value, res.VariationType, res.Failed, res.Version, "SERVER",
		evalCtx.ExtractGOFFProtectedFields().ExporterMetadata,
	))
}

// AllFlagsState evaluates every flag of the flag set for a context. A nil flag set (not available) answers like an
// empty one: valid, no flags.
func (e *Evaluator) AllFlagsState(set *FlagSet, evalCtx ffcontext.Context) flagstate.AllFlags {
	return e.GetFlagStates(set, evalCtx, nil)
}

// GetFlagStates evaluates the flags named in flagsToEvaluate, or every flag when the list is empty. A name that
// the flag set does not hold is left out. Object and array values are copies.
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

			addState(&states, key, evalCtx, flagCtx, flag)
		}

		return states
	}

	if set == nil {
		return states
	}

	for key, flag := range set.Flags {
		addState(&states, key, evalCtx, flagCtx, flag)
	}

	return states
}

func addState(
	states *flagstate.AllFlags, key string, evalCtx ffcontext.Context, flagCtx flagdef.Context,
	flag *flagdef.InternalFlag,
) {
	state := flagstate.FromFlagEvaluation(key, evalCtx, flagCtx, flag)
	state.Value = flagdef.CloneValue(state.Value)
	state.Metadata = ownMetadata(state.Metadata)

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
