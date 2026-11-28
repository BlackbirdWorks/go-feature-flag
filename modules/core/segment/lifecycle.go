package segment

import "context"

// The interfaces below are optional: a Provider may implement them, and the code that owns the provider (the relay
// proxy) reaches them with a type assertion, so it can start, stop and report on any store in the same way. The
// engine does not call them, and the Provider interface does not change.

// Initializer is implemented by a provider that has something to check or prepare before the first lookup. Init
// returns an error when the store cannot serve lookups.
type Initializer interface {
	Init(ctx context.Context) error
}

// Shutdowner is implemented by a provider that holds a resource that must be released. Shutdown is safe to call
// more than once: the second call returns nil.
type Shutdowner interface {
	Shutdown(ctx context.Context) error
}

// Existencer is implemented by a provider that can tell whether a segment is loaded. The answers are in the order
// of names: Member means loaded, NotMember means not, Unknown means the provider could not tell.
type Existencer interface {
	Exists(ctx context.Context, names []string) ([]State, error)
}

// Health is a snapshot of the health of a provider.
type Health struct {
	// Failure is the class of the failure of the last call, empty when it succeeded. It never holds an id or a key.
	Failure string
	// Ready is true once the provider answered a call.
	Ready bool
	// Closed is true after Shutdown.
	Closed bool
}

// HealthReporter is implemented by a provider that reports its health.
type HealthReporter interface {
	Health() Health
}

// StaleReporter is implemented by a provider that can answer from data that is older than it should be, for example a
// snapshot that could not be refreshed and keeps its last known good set. The evaluation reports the status
// [StatusStale] for the evaluations that it served while Stale is true; the answers are still used.
type StaleReporter interface {
	Stale() bool
}
