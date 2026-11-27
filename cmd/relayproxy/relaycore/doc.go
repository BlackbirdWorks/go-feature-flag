// Package relaycore assembles the config v2 relay core from a loaded bootstrap file: the connections, the object
// store of the control plane, the state directory, the manifest reader, the source that feeds the reconcile
// engine, the engine with its holder and evaluator, and the exporters and notifiers of the events package.
//
// A [Core] is a service object: [New] builds everything and starts nothing, [Core.Run] runs the loops until its
// context ends or [Core.Shutdown] is called, and [Core.Shutdown] stops them in order and drains the events within
// the deadline of its context. The package has no global state, imports no HTTP code and registers no route: the
// handlers of the relay take the [flagruntime.Holder] and the [flagruntime.Evaluator] from the core.
package relaycore
