package flagruntime

import "errors"

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
		return nil, RouteUnknown
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
