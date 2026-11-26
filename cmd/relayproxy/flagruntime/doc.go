// Package flagruntime holds the immutable runtime of the relay proxy and the reconcile engine that replaces it.
//
// A [Runtime] is built once from one verified snapshot of the published content ([Content]) and is never
// changed afterwards: nothing may mutate a Runtime, a [FlagSet], a [Status] or the flags they hold. A reader
// calls [Holder.Load] once and uses the result for the whole request; it never sees a mix of two generations.
//
// The [Builder] turns a Content into a Runtime with structural sharing: a flags object is parsed once per
// checksum, a flag set whose definition and sources did not change is carried over by pointer, and the flags
// of a source object are shared by every flag set that lists it. The [Engine] owns one goroutine, the only
// writer of the Holder. Work reaches it through channels (a latest-wins channel of Content, a latest-wins
// channel of bootstrap configurations), and every round ends with one atomic pointer store.
//
// The package is built beside the running relay: nothing in the relay reads it yet. The relay keeps running on
// the version 1 configuration until the switch to config v2.
//
// The values of variations are shared between flag sets (and between generations). A caller that gets a value
// out of a flag must treat it as read-only; the evaluator clones what it hands out.
package flagruntime
