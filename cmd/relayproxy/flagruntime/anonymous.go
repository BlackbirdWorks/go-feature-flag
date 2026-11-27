package flagruntime

import (
	"context"
	"log/slog"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// StateMissing is the state of the anonymous flag set that the generation does not hold.
const StateMissing SetState = "missing"

// AnonymousStatus is the status of the flag set of the requests without a key.
type AnonymousStatus struct {
	// FlagSet is the name written in the bootstrap file.
	FlagSet string `json:"flagSet"`
	// State is the state of that flag set, or "missing" when the generation has no flag set of that name.
	State SetState `json:"state"`
}

// routeAnonymous routes a request without a key: to the anonymous flag set when the bootstrap file names one, with
// the route a key of that flag set would get; else it is unknown. A key that is given never comes here.
func (r *Runtime) routeAnonymous() (*FlagSet, Route) {
	if r.Anonymous == nil {
		return nil, RouteUnknown
	}

	if r.Anonymous.State == StatePending {
		return r.Anonymous, RoutePending
	}

	return r.Anonymous, RouteFlagSet
}

// AnonymousName is the name of the anonymous flag set, empty when there is none.
func (r *Runtime) AnonymousName() string {
	if r.Anonymous == nil {
		return ""
	}

	return r.Anonymous.Name
}

// anonymousSet returns the flag set that serves the requests without a key: the one of the name, else an empty
// pending flag set of that name. Nil for no name.
func anonymousSet(name string, sets map[string]*FlagSet) *FlagSet {
	if name == "" {
		return nil
	}

	if set, ok := sets[name]; ok {
		return set
	}

	return &FlagSet{Name: name, State: StatePending, Flags: map[string]*flagdef.InternalFlag{}}
}

func anonymousStatus(anonymous *FlagSet, sets map[string]*FlagSet) *AnonymousStatus {
	if anonymous == nil {
		return nil
	}

	state := anonymous.State
	if _, ok := sets[anonymous.Name]; !ok {
		state = StateMissing
	}

	return &AnonymousStatus{FlagSet: anonymous.Name, State: state}
}

// warnAnonymous logs once per generation that the anonymous flag set is not held: its requests get the answer of a
// flag set that is not available until a generation publishes it.
func (e *Engine) warnAnonymous(ctx context.Context, previous, next *Runtime) {
	status := next.Status.Anonymous
	// generation 0 is the bootstrap file before the first manifest: no flag set is held yet, and none is missing.
	if status == nil || status.State != StateMissing || next.Generation == 0 {
		return
	}

	if prev := previous.Status.Anonymous; prev != nil && prev.FlagSet == status.FlagSet &&
		prev.State == StateMissing && previous.Generation == next.Generation {
		return
	}

	e.logger.LogAttrs(ctx, slog.LevelWarn, "the anonymous flag set is not in the generation",
		slog.String("flagSet", status.FlagSet), slog.Uint64("generation", next.Generation))
}
