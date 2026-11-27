// Package controlsource connects the manifest reader to the runtime: it turns every [manifestreader.Snapshot]
// into a [flagruntime.Content] and hands it to the engine.
//
// It is a separate package so that flagruntime does not import the reader (the runtime knows contents, not where
// they come from) and the reader does not import the runtime. The package imports no HTTP code: [Source.Status]
// joins the status of the reader with the result of the engine and the status tree of the running runtime, for the
// readiness and observability code that comes later.
//
// The package is built beside the running relay: nothing in the relay uses it yet.
package controlsource
