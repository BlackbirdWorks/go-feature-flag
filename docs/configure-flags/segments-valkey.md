# Segments in Valkey

Two Go packages serve segments from Valkey (or Redis 6.2 or later: the store needs `SMISMEMBER`, or `EVAL` for the atomic mode):

- `modules/core/segment/valkeysegment`: a `segment.Provider` that reads the members with go-redis. It only reads, so a read-only ACL user is enough.
- `modules/core/segment/resolver`: a `segment.Provider` that wraps any provider with a cache, coalescing and a circuit breaker for each segment.

See [Segments](segments.md) for the rule syntax and the three answers (member, not member, unknown). The relay proxy does not use these packages yet: the `segments` setting, the connection wiring and the metrics come with the relay wiring (issue 16).

## Data model

A segment is either a set (default) or one key per member.

| Layout | Key | Lookup |
|---|---|---|
| `set` | `<prefix>{<name>}` (the default) or the key you set | `SMISMEMBER key id...` |
| `keys` | `<prefix><name>:<id>` (the default) or `<your prefix><id>` | `EXISTS key` for each id |

- The default prefix is `semaphore:segment:`. The `{name}` is a hash tag: a temporary key with the same tag (`semaphore:segment:{beta}:tmp`) is in the same cluster slot, so `RENAME` works.
- A segment that is not in the provider options is never looked up (its lookups stay unknown). A flag cannot choose a key.
- With the `keys` layout an empty id is not looked up (it stays unknown): the key would be the bare prefix.
- Members are compared byte for byte. The provider does not normalise or hash ids; the resolver can trim and lower them (see below). Hashing is not done yet.

## Load members

```shell
# set layout
valkey-cli SADD 'semaphore:segment:{beta}' alice carol
# keys layout
valkey-cli SET flag:alice 1
```

To replace a large set without readers seeing a half-built set, build it under a temporary key with the same hash tag, then swap:

```shell
valkey-cli DEL 'semaphore:segment:{beta}:tmp'
valkey-cli SADD 'semaphore:segment:{beta}:tmp' alice carol dave   # in batches
valkey-cli RENAME 'semaphore:segment:{beta}:tmp' 'semaphore:segment:{beta}'
```

`RENAME` is atomic: a reader sees the old set or the new one, never a missing key. The `semaphore-cli segment` tooling (issue 17) will do this for you. Deleting the live key and then rebuilding it is not atomic: for a moment the segment is not loaded (see Missing key).

## Use it

```go
store, err := valkeysegment.New(redisClient, valkeysegment.Options{ // a redis.UniversalClient
    Segments: map[string]valkeysegment.SegmentOptions{
        "beta":    {},                                               // set at semaphore:segment:{beta}
        "flagged": {Layout: valkeysegment.LayoutKeys, Key: "flag:"}, // keys flag:<id>
    },
    Timeout: 50 * time.Millisecond, // per call, under the context of the caller
})
// ...
provider, err := resolver.New(store, &resolver.Options{
    Segments: map[string]resolver.SegmentOptions{
        "beta": {TTL: time.Minute, NegativeTTL: 10 * time.Second, MaxEntries: 50000},
    },
})
client, err := ffclient.New(&ffclient.Config{ /* ... */ Segments: catalog, SegmentProvider: provider })
```

- `valkeysegment.New` takes any go-redis client (a single node, a failover set or a cluster), so the relay can share its connection. `Dial` builds the client from `redis.UniversalOptions`.
- The resolver is optional: without it every call goes to Valkey. With it, a call is: normalise, cache, breaker, one coalesced call to the store.

## Defaults

| Setting | Default |
|---|---|
| `valkeysegment` `Timeout` (one call) | 100 ms |
| `valkeysegment` `KeyPrefix` | `semaphore:segment:` |
| `valkeysegment` `MissingKey` | `unknown` |
| `valkeysegment` `Atomic` | off |
| `resolver` `TTL` (members) | 30 s |
| `resolver` `NegativeTTL` (non members) | 5 s |
| `resolver` `MaxEntries` (each segment) | 10000 |
| `resolver` `Shards` (locks of the cache of a segment) | 16 |
| `resolver` `BreakerFailures` (consecutive failed calls) | 5 |
| `resolver` `BreakerCooldown` | 10 s |
| `resolver` `CallTimeout` (the shared call) | 2 s |
| `resolver` `Normalize` | `none` (the other value is `trimLower`) |

`Options.Default` sets the values of the segments that `Options.Segments` does not name.

## One round trip

All the lookups of a call go out in one pipeline, grouped by segment: one `SMISMEMBER` for all the ids of a set segment, one `EXISTS` per id of a keys segment. A rule makes one call, so a rule is one round trip on a single node. On a cluster the client splits a pipeline by node, so it is one round trip per node.

## Cache

- The cache of a segment is a bounded LRU: at most `MaxEntries` answers (entries, not bytes), split over `Shards` locks. A hit does not allocate. With 16 locks the parallel hit path keeps its speed from 1 to 8 CPUs; with one lock it slows down by about half.
- A member is cached for `TTL`, a non member for the shorter `NegativeTTL`. **Unknown answers and errors are never cached.**
- Consistency is eventual: a change of the members shows after at most the TTL. `Resolver.Invalidate(segment)` drops the answers of a segment at once, and the answers of the calls that are still in flight; it is the hook for a change notification (the sync of the tooling, issue 17, will call it).
- `NoCache` turns the cache of a segment off.

## Coalescing

When several callers look the same (segment, id) up at the same time, one of them asks the store and the others wait for its answer: eight concurrent callers, one round trip. The shared call does not depend on the context of any caller: a caller whose context ends leaves at once with unknown lookups and an error that wraps `ErrWaiting` and the context error, and the call goes on for the others. The shared call has its own `CallTimeout`.

## Circuit breaker

Each segment has a breaker. After `BreakerFailures` consecutive failed calls it opens: the lookups are unknown at once, the store is not called, and `Contains` returns `ErrCircuitOpen`. After `BreakerCooldown` one call, the probe, goes through; if it succeeds the breaker closes, if not it opens again for another cool-down. A call fails for a segment when it returned an error and left a lookup of that segment unanswered. `NoBreaker` turns it off.

`Resolver.Status()` returns the breaker state and the number of cached answers of each segment, and `Observer` receives each change of state.

## Missing key

A set that does not exist is not the same as an empty segment, and Valkey deletes a set that becomes empty. The provider therefore adds an `EXISTS` of the set key to the same pipeline:

| `MissingKey` | A non-member of a set whose key does not exist is |
|---|---|
| `unknown` (default) | unknown: the segment is not loaded, and the engine applies the failure policy |
| `notMember` | not a member, and the `EXISTS` is not sent |

A member is always a member. The `keys` layout cannot tell a missing member from a segment that is not loaded: a missing key is not a member.

The `SMISMEMBER` and the `EXISTS` of a pipeline are two commands: if the key is deleted between them, a non-member reads as unknown (the safe direction). `Atomic` closes that gap: each set segment is then one `EVAL` of a short Lua script that checks the key and the ids in one atomic step. On a cluster the script reads one key, so it runs on the node of that key. It costs about 20% more latency in a local measurement (less allocation). A swap with `RENAME` never leaves the key missing, so it does not need `Atomic`.

## Failure behaviour

The provider and the resolver never apply a failure policy: they leave what they cannot answer unknown and the engine applies `onError` of the segment definition and of the rule.

| Cause | Lookups | Error |
|---|---|---|
| Store down, connection broken | all unknown | `valkeysegment.ErrUnavailable` |
| The per-call timeout or a network timeout | all unknown | `valkeysegment.ErrTimeout` |
| The caller's context ended (before or during the call) | all unknown | `valkeysegment.ErrCanceled`, or `resolver.ErrWaiting` when it ended while waiting |
| The store replied with an error (no permission, wrong type, loading) for one command | the lookups of that command are unknown, the others keep their answer | `valkeysegment.ErrServer` |
| A reply that does not fit the command | the lookups of that command are unknown | `valkeysegment.ErrProtocol` |
| The breaker of the segment is open | unknown, no call | `resolver.ErrCircuitOpen` |
| Segment not in the options | unknown | none |
| Set key missing (default) | non-members unknown | none |

The errors hold a class and counts, never an id, a key or an address. The engine then reports the status `STORE_ERROR` for the evaluation. With Valkey down, a request takes at most the deadline of its context plus the time to return: with an open breaker it returns without a network call.

## Lifecycle

`valkeysegment.Provider` has `Init` (a PING under the timeout), `Status` (ready, closed, class of the last failure), `Exists` (is the key of each segment there) and `Shutdown`. The resolver forwards `Init`, `Exists` and `Shutdown` to the store when it has them, and `Shutdown` waits for the calls in flight. They are methods of the types, not of the `segment.Provider` interface.

## Metrics

The relay proxy will export the observations of the resolver (issue 16 and 18). The names below are the plan, not available yet: labels carry the segment name, never an id.

| Metric | From |
|---|---|
| `semaphore_segment_lookups_total{segment,result}` | `CallObservation` (lookups, hits, misses, unknown) |
| `semaphore_segment_lookup_duration_seconds{segment}` | `CallObservation.Duration` |
| `semaphore_segment_cache_total{segment,outcome}` | hits, misses, coalesced |
| `semaphore_segment_unknown_total{segment,reason}` | short circuits and failures by class |
| `semaphore_segment_breaker_state{segment}` | `BreakerEvent` and `Status()` |

## Limits and sizing

- A call costs the number of ids: one `SMISMEMBER` takes them all as arguments. A rule looks up one attribute value per term, so calls are small.
- The cache holds `MaxEntries` answers for each segment, about 100 to 200 bytes each with short ids. Sizing example: 20 segments at the default 10000 entries is at most 200000 entries, about 20 to 40 MB, whatever the size of the segments in Valkey.
- A lookup that is not cached costs one round trip (about 50 microseconds on a local network). With a 30 s TTL, a hot id is asked about twice a minute, however many requests use it.
- The ids that a segment never saw are cached as non members for `NegativeTTL`; a flood of distinct ids fills the LRU and evicts the useful entries, which is what `MaxEntries` bounds.
- Needs `SMISMEMBER` (Valkey 7.2 or Redis 6.2), or `EVAL` for `Atomic`.

## Cluster notes

- The default set keys have a hash tag for each segment, so the lookups of one segment go to one node, and a temporary key with the same tag can be renamed onto the live key. A temporary key without the tag cannot (`CROSSSLOT`).
- A call over several segments is a pipeline that the client splits by node: it works across slots, with one round trip per node.
- The keys of the `keys` layout spread over the nodes.
- The tests of the package start a real three-node cluster and check these rules.
