// Package segmentadmin is the write side of the dynamic segments: the code that loads members into a Valkey or Redis
// store, the counterpart of the read side in valkeysegment and redissegment. It holds the [Store] interface (one
// implementation for valkey-go, one for go-redis), the streaming import with its atomic swap, the small edits and the
// statistics of a segment.
//
// A member is stored as [resolver.NormalizeID] returns it and under the key that [redissegment.DefaultKey] (or the
// key of the definition) names, which are the functions the relay uses for a lookup: the two sides cannot drift. No
// member id is ever put in an error or a report: only counts and line numbers are.
package segmentadmin
