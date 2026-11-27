package corehttp

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/events"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/flagstate"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/model"
)

// The errors of the lookup of a flag set. The texts are the ones of the v1 flag set manager: the handlers answer
// them in the body of the 400 of a key that has no flag set.
var (
	errNoAPIKey        = errors.New("no API key provided")
	errFlagSetNotFound = errors.New("flagset not found for API key")
)

// Source is what the adapter needs of a core.
type Source interface {
	// Holder returns the holder of the current runtime.
	Holder() *flagruntime.Holder
	// Evaluator returns the evaluator of the flag sets.
	Evaluator() *flagruntime.Evaluator
	// Collect hands a feature event to the exporters.
	Collect(event events.Event) bool
}

// Adapter implements the seam of the HTTP layer on a core: it finds the flag set of an API key in the current runtime
// and evaluates against it. It is safe for concurrent use and owns no goroutine.
type Adapter struct {
	holder  *flagruntime.Holder
	eval    *flagruntime.Evaluator
	collect func(events.Event) bool
	// table is the flag set views of the runtime that was last served: a view is made once per flag set, so
	// resolving a key does not allocate.
	table atomic.Pointer[viewTable]
}

var (
	_ service.FlagSetResolver = (*Adapter)(nil)
	_ service.KeySource       = (*Adapter)(nil)
)

// NewAdapter returns the adapter of a core.
func NewAdapter(src Source) *Adapter {
	return &Adapter{holder: src.Holder(), eval: src.Evaluator(), collect: src.Collect}
}

// viewTable holds the views of the flag sets of one runtime.
type viewTable struct {
	rt    *flagruntime.Runtime
	views map[*flagruntime.FlagSet]*view
}

// views returns the views of the flag sets of the runtime. A flag set that did not change keeps its pointer across
// the generations, so its view is reused.
func (a *Adapter) views(rt *flagruntime.Runtime) map[*flagruntime.FlagSet]*view {
	prev := a.table.Load()
	if prev != nil && prev.rt == rt {
		return prev.views
	}

	views := make(map[*flagruntime.FlagSet]*view, len(rt.FlagSets))

	for _, set := range rt.FlagSets {
		if prev != nil {
			if old, ok := prev.views[set]; ok {
				views[set] = old

				continue
			}
		}

		views[set] = &view{adapter: a, set: set}
	}

	a.table.Store(&viewTable{rt: rt, views: views})

	return views
}

// lookup routes a key to the view of its flag set. A flag set that is known but not loaded (pending) is routed too:
// it serves no flags. An admin key, an unknown key and an empty key have no flag set.
func (a *Adapter) lookup(apiKey string) (*view, error) {
	if apiKey == "" {
		return nil, errNoAPIKey
	}

	rt := a.holder.Load()

	set, route := rt.Route(apiKey)
	if route != flagruntime.RouteFlagSet && route != flagruntime.RoutePending {
		return nil, errFlagSetNotFound
	}

	found, ok := a.views(rt)[set]
	if !ok {
		return nil, errFlagSetNotFound
	}

	return found, nil
}

// FlagSet returns the flag set of the API key.
func (a *Adapter) FlagSet(apiKey string) (service.FlagSet, error) {
	found, err := a.lookup(apiKey)
	if err != nil {
		return nil, err
	}

	return found, nil
}

// FlagSetName returns the name of the flag set of the API key.
func (a *Adapter) FlagSetName(apiKey string) (string, error) {
	found, err := a.lookup(apiKey)
	if err != nil {
		return "", err
	}

	return found.set.Name, nil
}

// IsDefaultFlagSet is false: a core always routes the keys to flag sets.
func (*Adapter) IsDefaultFlagSet() bool { return false }

// AuthenticationEnabled is true: a core has no anonymous access.
func (*Adapter) AuthenticationEnabled() bool { return true }

// KeyExists reports whether the key is a flag set key or an admin key.
func (a *Adapter) KeyExists(apiKey string) bool {
	_, route := a.holder.Load().Route(apiKey)

	return route != flagruntime.RouteUnknown
}

// AdminKeyExists reports whether the key is an admin key.
func (a *Adapter) AdminKeyExists(apiKey string) bool {
	_, route := a.holder.Load().Route(apiKey)

	return route == flagruntime.RouteAdmin
}

// view is the flag set of one runtime, the handlers read from.
type view struct {
	adapter *Adapter
	set     *flagruntime.FlagSet
}

var _ service.FlagSet = (*view)(nil)

// RawVariation evaluates one flag.
func (v *view) RawVariation(flagKey string, evalCtx ffcontext.Context, defaultValue any) (model.RawVarResult, error) {
	res, err := v.adapter.eval.RawVariation(v.set, flagKey, evalCtx, defaultValue)

	return res, err //nolint:wrapcheck // the error of the evaluation, as the v1 client returns it
}

// GetFlagStates evaluates the listed flags.
func (v *view) GetFlagStates(evalCtx ffcontext.Context, flagsToEvaluate []string) flagstate.AllFlags {
	return v.adapter.eval.GetFlagStates(v.set, evalCtx, flagsToEvaluate)
}

// AllFlagsState evaluates every flag.
func (v *view) AllFlagsState(evalCtx ffcontext.Context) flagstate.AllFlags {
	return v.adapter.eval.AllFlagsState(v.set, evalCtx)
}

// GetFlagsFromCache returns the flags of the flag set. The flags are the ones the evaluations use: the caller reads
// them and never changes them.
func (v *view) GetFlagsFromCache() (map[string]flagdef.Flag, error) {
	return v.set.Configuration(nil), nil
}

// GetFlagsFromCacheWithContext is GetFlagsFromCache.
func (v *view) GetFlagsFromCacheWithContext(_ context.Context) (map[string]flagdef.Flag, error) {
	return v.set.Configuration(nil), nil
}

// GetCacheRefreshDate is the date the flag set was last modified.
func (v *view) GetCacheRefreshDate() time.Time { return v.set.ModifiedAt }

// GetEvaluationContextEnrichment returns a copy of the enrichment of the flag set.
func (v *view) GetEvaluationContextEnrichment() map[string]any {
	return v.adapter.eval.EvaluationContextEnrichment(v.set)
}

// CollectEventData hands the event to the exporters.
func (v *view) CollectEventData(event *exporter.FeatureEvent) { v.adapter.collect(event) }

// CollectTrackingEventData drops the event: the exporters of a core take feature events only.
func (*view) CollectTrackingEventData(*exporter.TrackingEvent) {}

// ForceRefresh is false: a core follows its control plane by polling and does not refresh on request.
func (*view) ForceRefresh() bool { return false }
