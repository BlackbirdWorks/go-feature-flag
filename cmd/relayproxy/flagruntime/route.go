package flagruntime

import (
	"context"
	"errors"
	"log/slog"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// Route is what an API key resolves to in one [Runtime]. The HTTP layer maps it to a status code; this package
// never imports HTTP.
type Route uint8

// The routes of an API key.
const (
	// RouteUnknown: no flag set and no admin key has this key (HTTP 401). A key of a removed flag set is
	// unknown from the generation that removed it.
	RouteUnknown Route = iota
	// RouteFlagSet: the key routes to a flag set that serves its flags.
	RouteFlagSet
	// RoutePending: the key routes to a flag set that is not available (state pending: nothing loaded yet, or
	// its entry was invalid and it never had a definition). Follow upstream: a single evaluation answers
	// FLAG_NOT_FOUND, a bulk evaluation and the listings answer 200 with an empty set. Never 503. The returned
	// flag set holds no flag, so the evaluator gives these answers by itself.
	RoutePending
	// RouteAdmin: the key is an admin key of the bootstrap file; it routes to no flag set.
	RouteAdmin
)

// Sentinel errors of the routes that are not served.
var (
	// ErrUnknownKey is the error of [RouteUnknown].
	ErrUnknownKey = errors.New("runtime: unknown API key")
	// ErrFlagSetUnavailable is the error of [RoutePending]. A caller that serves an answer for the pending flag
	// set uses it to say "flag set is not available" in the error details of a single evaluation.
	ErrFlagSetUnavailable = errors.New("runtime: flag set is not available")
)

// String returns the name of the route.
func (r Route) String() string {
	switch r {
	case RouteUnknown:
		return "unknown"
	case RouteFlagSet:
		return "flagSet"
	case RoutePending:
		return "pending"
	case RouteAdmin:
		return "admin"
	}

	return "unknown"
}

// Err returns nil for a route that serves flags (or is an admin key), [ErrUnknownKey] for RouteUnknown and
// [ErrFlagSetUnavailable] for RoutePending. The errors are sentinels: the call does not allocate and never holds
// a key.
func (r Route) Err() error {
	switch r {
	case RouteFlagSet, RouteAdmin:
		return nil
	case RoutePending:
		return ErrFlagSetUnavailable
	case RouteUnknown:
		return ErrUnknownKey
	}

	return ErrUnknownKey
}

// Route resolves an API key: the flag set it routes to (nil unless the route is [RouteFlagSet] or [RoutePending])
// and the route. A flag set key wins over an admin key.
//
// The lookup is one read of two immutable maps: no lock, no allocation. Go compares the key with the stored key
// only after the hash of the bucket matched, so the time depends on the key as the time of any map lookup does;
// the keys are random identifiers (UUIDs) and the relay answers the same 401 whatever the mismatch, so the lookup
// is no weaker than the map lookup of version 1. No key is copied, logged or put in an error.
func (r *Runtime) Route(key string) (*FlagSet, Route) {
	if key == "" {
		return r.routeAnonymous()
	}

	if fs, ok := r.ByKey[key]; ok {
		if fs.State == StatePending {
			return fs, RoutePending
		}

		return fs, RouteFlagSet
	}

	if _, ok := r.AdminKeys[key]; ok {
		return nil, RouteAdmin
	}

	return nil, RouteUnknown
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
