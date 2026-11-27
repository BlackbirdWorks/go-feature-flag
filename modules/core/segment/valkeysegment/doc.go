// Package valkeysegment is the [segment.Provider] of segment type [segment.TypeValkey]. It answers
// segment membership lookups from a Valkey (or Redis 6.2 and later) store with valkey-go.
//
// All the lookups of one call go out in one DoMulti: the client writes the commands of the call to the connection
// together and reads their replies in order, so one call is one round trip on a single node (one per node on a
// cluster). Concurrent calls share a connection and the client batches their writes (automatic pipelining). A
// segment is either a set of members read with SMISMEMBER (layout "set") or one key per member read with EXISTS
// (layout "keys"). The provider only reads, so a read-only ACL user is enough. Client-side caching of the client is
// off: the answers are not cached here, the resolver does that.
//
// The layouts, the options of a segment, the failure classes and the observer are the ones of package
// redissegment (the go-redis provider), so the two providers can be swapped. The provider reports what it could
// not answer as [segment.Unknown] and never applies a failure policy. A call that fails, in whole or in part,
// returns a *[LookupError] that names the class and the counts only, never an id, a key or an address. A reply that
// is not the one the command returns, or a stream that cannot be parsed, means no answer of the call is kept.
//
// valkey-go watches the deadline of a call that is alone on its connection, not its cancel: a call ends at
// Options.Timeout at the latest, and is reported as canceled when the context of the caller ended.
//
// Shutdown stops new calls (they return [ErrClosed]), waits for the calls in flight until its own context ends,
// and then closes the client when the provider owns it ([Dial]), which ends the calls still in flight.
package valkeysegment
