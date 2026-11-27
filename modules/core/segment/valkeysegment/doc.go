// Package valkeysegment is a [segment.Provider] that answers segment membership lookups from Valkey (or any
// Redis-compatible store) with go-redis.
//
// All the lookups of one call go out in one pipeline, so one call is one round trip on a single node (one per node
// on a cluster). A segment is either a set of members read with SMISMEMBER (layout "set") or one key per member
// read with EXISTS (layout "keys"). The provider only reads: it never writes, so a read-only ACL user is enough.
//
// The provider reports what it could not answer as [segment.Unknown] and never applies a failure policy: the
// engine does (segment.Definition.OnError and the onError of the rule). A call that fails, in whole or in part,
// returns a *[LookupError] that names the class of the failure and the counts only, never an id or a key.
package valkeysegment
