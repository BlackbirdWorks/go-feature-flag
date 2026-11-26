// Package connection builds and shares the AWS clients of the relay: exactly one client per named connection
// (one bucket or endpoint, one region, one credentials identity) per process.
//
// A [Spec] describes a connection. A [Factory] holds the specs by name, builds the client of a connection on
// first use, hands the same client to every caller, and on a reload ([Factory.Replace]) keeps the client of a
// connection whose [Spec.Identity] did not change. The AWS backed kinds live in this
// package; a backend that is not AWS gets its own spec type and factory method.
//
// Every attempt of every request made through a client is reported to the [RequestObserver] of the
// factory, with the closed set of results defined in this package. The package does not import Prometheus:
// the relay maps the observer to its metric.
//
// Region and endpoint come from the spec only, never from the process environment or the shared config files,
// and no state is global: two connections can use two endpoints in one process. The only environment access
// is the one of the AWS SDK for credentials of type chain (and assumeRole without source keys). The HTTP
// clients the factory builds do not follow redirects. An observation is one attempt of the SDK: the Go transport
// may replay an idempotent request, only when the first send got no response, without a new observation.
package connection
