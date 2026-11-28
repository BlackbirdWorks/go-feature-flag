// Package segmentstore tells the relay which kinds of connection of the bootstrap file can serve segments, and in
// which modes. It is the only place of the relay that names the kinds of store: the reconcile engine asks the
// [flagruntime.SegmentStores] it returns and never looks at a kind.
//
// Nothing is opened here. A kind is described by the type that serves it ([redissegment.Provider] for redis,
// [valkeysegment.Provider] for valkey); the types are only inspected for the [segment] interfaces they implement, so a
// store that gains a mode is seen without a change to this package.
package segmentstore
