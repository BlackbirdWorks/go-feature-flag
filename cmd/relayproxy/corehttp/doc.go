// Package corehttp serves the HTTP API of the relay proxy (the evaluation endpoints, the flag configuration, the
// flag change, the collection of events and the streams of changes) from a [relaycore.Core]. The handlers see the
// core through [service.FlagSetResolver] and [service.FlagSet], which [Adapter] implements on the holder of the
// runtime and the evaluator.
package corehttp
