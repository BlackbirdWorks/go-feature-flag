// Package redissegment is a [segment.Provider] that answers segment membership lookups from a Redis or Valkey
// store with go-redis (segment type [segment.TypeRedis]; the valkey-go provider, type [segment.TypeValkey], is a
// separate package).
//
// All the lookups of one call go out in one pipeline (one more after a NOSCRIPT answer), so one call is one round trip
// on a single node (one per node on a cluster). A segment is either a set of members read with SMISMEMBER (layout
// "set") or one key per member read with EXISTS (layout "keys"). The provider only reads: it never writes, so a
// read-only ACL user is enough.
//
// The provider reports what it could not answer as [segment.Unknown] and never applies a failure policy: the
// engine does (segment.Definition.OnError and the onError of the rule). A call that fails, in whole or in part,
// returns a *[LookupError] that names the class of the failure and the counts only, never an id or a key. A panic of
// the client is recovered and counts as an unavailable store: no answer of that call is kept.
//
// A go-redis client passed to [New] must have ContextTimeoutEnabled set, or the socket ignores the deadline of a call:
// New refuses one that exposes its options and has it off. [Dial] sets it on a copy of the options. Init probes the
// commands that the configured layouts and mode will use, on keys of the configured namespace.
//
// Shutdown stops new calls (Contains, Init and Exists return [ErrClosed]), waits for the calls in flight until its own
// context ends, and then ends them: no command is sent after it returns.
package redissegment
