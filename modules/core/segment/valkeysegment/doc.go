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
// [Dial] makes the client pipeline in the background even for a call that is alone on its connection
// (AlwaysPipelining). That path ends a call when the context ends, and keeps the replies that were read when the
// connection fails inside a batch (the others are Unknown with the transport class). valkey-go's synchronous path
// does neither: it loses the whole batch on a failure and sees only the deadline, not a cancel. A client passed to
// [New] must be created the same way, see [Client]. [Dial] also raises ClientOption.BlockingPipeline: valkey-go moves
// a batch of 2000 commands or more to a dedicated connection, where a cancel waits for the connection to close.
//
// Limits: a server or proxy that sends a reply nobody asked for makes valkey-go panic in its reader goroutine, which
// ends the process. valkey-go does this on every connection that pipelines in the background, which any concurrent
// load starts, so it is a property of the library and not of AlwaysPipelining. The go-redis provider (type redis)
// drops such a connection and returns an error: use it when the store sits behind a proxy that you do not trust.
//
// Shutdown stops new calls (they return [ErrClosed]), waits for the calls in flight until its own context ends,
// and then ends them: the call in flight returns the canceled class. It also closes the client when the provider
// owns it ([Dial]).
package valkeysegment
