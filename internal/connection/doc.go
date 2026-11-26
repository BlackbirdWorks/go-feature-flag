// Package connection builds and shares the AWS clients of the relay: exactly one client per named connection
// (one bucket or endpoint, one region, one credentials identity) per process.
//
// A [Spec] describes a connection. A [Factory] holds the specs by name, builds the client of a connection on
// first use, hands the same client to every caller, and on a reload ([Factory.Replace]) keeps the client of a
// connection whose [Spec.Identity] did not change. Connection kinds are an open set: a new kind is a new
// [Spec] implementation, callers keep using the factory.
//
// Every attempt of every request made through a client is reported to the [RequestObserver] of the
// factory, with the closed set of results defined in this package. The package does not import Prometheus:
// the relay maps the observer to its metric.
//
// Nothing here reads the process environment itself and no state is global: two connections can use two
// endpoints in one process. The only environment access is the one of the AWS SDK for a connection whose
// credentials type is chain.
package connection
