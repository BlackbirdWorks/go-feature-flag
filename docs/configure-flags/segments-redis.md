# Segments - dynamic: Redis

Three Go packages serve dynamic segments from Redis or Valkey with go-redis (Redis 6.2 or later, Valkey 7.2 or later: the store needs `SMISMEMBER`, and `EVALSHA` or `EVAL` for the atomic mode):

- `modules/core/segment/redissegment`: a `segment.Provider` that reads the members with go-redis. It only reads, so a read-only ACL user is enough (see ACL below).
- `modules/core/segment/resolver`: a `segment.Provider` that wraps any provider with a cache, coalescing and a circuit breaker for each segment.
- `modules/core/segment/segmenttest`: the conformance suite of a `segment.Provider`, for the providers that come later.

Looking for the `valkey` type (valkey-go, automatic pipelining)? See [Segments - dynamic](segments-dynamic.md) for how to choose between the two types.

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
redis-cli SADD 'semaphore:segment:{beta}' alice carol
# keys layout
redis-cli SET flag:alice 1
```

To replace a large set without readers seeing a half-built set, build it under a temporary key with the same hash tag, then swap:

```shell
redis-cli DEL 'semaphore:segment:{beta}:tmp'
redis-cli SADD 'semaphore:segment:{beta}:tmp' alice carol dave   # in batches
redis-cli RENAME 'semaphore:segment:{beta}:tmp' 'semaphore:segment:{beta}'
```

`RENAME` is atomic: a reader sees the old set or the new one, never a missing key. The `semaphore-cli segment` tooling (issue 17) will do this for you. Deleting the live key and then rebuilding it is not atomic: for a moment the segment is not loaded (see Missing key).

## Use it

```go
store, err := redissegment.New(redisClient, &redissegment.Options{ // a go-redis client with ContextTimeoutEnabled
    Segments: map[string]redissegment.SegmentOptions{
        "beta":    {},                                               // set at semaphore:segment:{beta}
        "flagged": {Layout: redissegment.LayoutKeys, Key: "flag:"}, // keys flag:<id>
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

- `redissegment.New` takes any go-redis client (a single node, a failover set or a cluster), so the relay can share its connection. `Dial` builds the client from `redis.UniversalOptions`, sets `ContextTimeoutEnabled` on a copy of them, and the provider owns the client.
- A client passed to `New` must have `ContextTimeoutEnabled` set and must not disable its read or write timeout (`ReadTimeout` or `WriteTimeout` of `-2`; the value that the client reports must not be negative, for a node and for a cluster client): without them the socket ignores the deadline of the call, and `New` returns an error that wraps `ErrInvalidOptions` (`ErrContextTimeoutDisabled` or `ErrSocketDeadlineDisabled`). A client that does not show its options is accepted.
- The resolver is optional: without it every call goes to the store. With it, a call is: normalise, cache, breaker, one coalesced call to the store.

## Defaults

| Setting | Default |
|---|---|
| `redissegment` `Timeout` (one call) | 100 ms |
| `redissegment` `KeyPrefix` | `semaphore:segment:` |
| `redissegment` `MissingKey` | `unknown` |
| `redissegment` `Atomic` | off |
| `resolver` `TTL` (members) | 30 s |
| `resolver` `NegativeTTL` (non members) | 5 s |
| `resolver` `MaxEntries` (each segment) | 10000 |
| `resolver` `Shards` (locks of the cache of a segment) | 16 |
| `resolver` `BreakerFailures` (consecutive failed calls) | 5 |
| `resolver` `BreakerCooldown` | 10 s |
| `resolver` `CallTimeout` (the shared call) | 2 s |
| `resolver` `Normalize` | `none` (the other value is `trimLower`) |
| `resolver` and `redissegment` `MaxIDBytes` (longest id that is looked up) | 512 |
| `redissegment` `MaxIDsPerCommand` (ids in one `SMISMEMBER`; not applied to `Atomic`) | 1000 |
| `redissegment` `MaxInitProbes` (namespaces that `Init` probes) | 16 |

`Options.Default` sets the values of the segments that `Options.Segments` does not name.

## One round trip

All the lookups of a call go out in one pipeline, grouped by segment: one `SMISMEMBER` for all the ids of a set segment, one `EXISTS` per id of a keys segment. A rule makes one call, so a rule is one round trip on a single node. On a cluster the client splits a pipeline by node, so it is one round trip per node.

## Cache

- The cache of a segment is a bounded LRU: **at most `MaxEntries` answers, exactly** (entries, not bytes). The bound is split over `Shards` locks, the remainder going to the first shards, so the shards add up to the bound and there are never more shards than entries.
- A hit does not allocate (a test pins it). In a benchmark on a loaded machine with 4 to 8 parallel callers, one lock was about twice as slow for each call as sixteen; with one caller the two are the same. Re-measure on your own hardware before you tune `Shards`.
- A member is cached for `TTL`, a non member for the shorter `NegativeTTL`. **Unknown answers and errors are never cached.**
- Consistency is eventual: a change of the members shows after at most the TTL. `Resolver.Invalidate(segment)` drops the answers of a segment at once, the memory of a segment that was not loaded, and detaches the calls that are in flight: their answers are not cached, and a caller that comes after `Invalidate` makes a call of its own instead of joining one that started before. It is the hook for a change notification (the sync of the tooling, issue 17, will call it).
- `NoCache` turns the cache of a segment off.

### Memory

The memory of the cache is **segments x `MaxEntries` x (about 110 bytes + the bytes of the id)**: the map slot, the list node, the answer and the expiry cost about 110 bytes (measured, and a test pins it under 200), and the entry keeps the id itself. So an entry costs about 130 bytes with 16 byte ids, 175 with 64 byte ids and 620 with 512 byte ids (`MaxIDBytes` is the ceiling). 20 segments at the default 10000 entries with 64 byte ids is at most 200000 entries, about 35 MB. There is no cap across segments: a flag set with many segments multiplies the bound. A segment that is not named in the options gets a cache with the `Default` settings the first time a flag looks it up, and that state lives as long as the resolver: freeing the state of a segment that left the configuration is not done (a new `Resolver` for a new configuration replaces the old one).

### Ids that are not looked up

- An id longer than `MaxIDBytes` stays **Unknown**, is **never sent to the store and never cached**, and is counted in the observation (`Oversized`, reason `oversized`). The call returns no error for it: the engine applies the failure policy of the segment, as for any lookup that cannot be answered. Unknown, not NotMember, so that a rule that denies fails closed.
- With `trimLower`, an id that is not valid UTF-8 stays Unknown too (reason `invalid`) and is not sent. Lowering would replace its invalid bytes with U+FFFD, and two different ids could meet a member that holds that character.
- `trimLower` trims the white space around the id and lowers it with the Unicode tables of Go (`strings.ToLower`, one rune at a time: it is not full case folding). The loader of the members must lower in exactly the same way. Use ASCII ids when you can.

## Coalescing

When several callers look the same (segment, id) up at the same time, one of them asks the store and the others wait for its answer: eight concurrent callers, one round trip. The shared call does not depend on the context of any caller: a caller whose context ends leaves at once with unknown lookups and an error that wraps `ErrWaiting` and the context error, and the call goes on for the others. The shared call has its own `CallTimeout`, and a caller also leaves when that timeout passes, whether or not the store honours its context. A store that ignores its context keeps running after that: `Status().Overdue` counts those calls, and `Shutdown` that ends before they do returns an error that names how many are still running.

## Circuit breaker

Each segment has a breaker. After `BreakerFailures` consecutive failed calls it opens: the lookups are unknown at once, the store is not called, and `Contains` returns `ErrCircuitOpen`. After `BreakerCooldown` one call, the probe, goes through; if it succeeds the breaker closes, if not it opens again for another cool-down. A call fails for a segment when it returned an error and left a lookup of that segment unanswered. A result carries the generation of the breaker it was allowed in: the result of a call that started before the breaker opened is not taken for the result of the probe. A set that is not loaded (the store answers without error and every lookup of the segment is unknown) is **neither a success nor a failure**: the breaker does not count it, and the segment is remembered as not loaded for `NegativeTTL`, so that it does not hit the store on every request (`Status()` and the observations count the lookups skipped that way; `Invalidate` clears the memory). `NoBreaker` turns the breaker off.

`Resolver.Status()` returns the breaker state and the number of cached answers of each segment, and `Observer` receives each change of state.

## Missing key

A set that does not exist is not the same as an empty segment, and Redis and Valkey delete a set that becomes empty. The provider therefore adds an `EXISTS` of the set key to the same pipeline:

| `MissingKey` | A non-member of a set whose key does not exist is |
|---|---|
| `unknown` (default) | unknown: the segment is not loaded, and the engine applies the failure policy |
| `notMember` | not a member, and the `EXISTS` is not sent |

A member is always a member. The `keys` layout cannot tell a missing member from a segment that is not loaded: a missing key is not a member.

The `SMISMEMBER` and the `EXISTS` of a pipeline are two commands, and the key can change between them, in both directions:

- The key is deleted between them: a non member reads as unknown (the safe direction).
- The key is created between them (a segment is loaded for the first time, or recreated after a `DEL`): the `SMISMEMBER` saw no key and answered no for everyone, the `EXISTS` then sees the key, and a lookup that is a member of the new set reads as **not a member**, until the answer leaves the cache (`NegativeTTL`) or `Invalidate` runs. It is never a false member.

`Atomic` closes both gaps: each set segment is then one `EVALSHA` of a short Lua script, whatever the number of ids (the limit of 1000 ids does not apply, so a set is never split over two scripts) that checks the key and the ids in one atomic step, on the node of the key. A `MULTI/EXEC` was tried first and does not work in one round trip on a cluster: a transaction that holds keys of several segments is refused with `CROSSSLOT`, and one transaction for each segment is one round trip for each. The script is called by its SHA, so its text is not sent on each call. A store that does not have it yet answers `NOSCRIPT`, and the provider sends the text of the script once with `EVAL` for those lookups (a second round trip, after a restart or a flush of the scripts). `Init` finds out whether the store has `EVAL_RO` and `EVALSHA_RO` (Redis 7 and later, Valkey 7.2 and later) and uses them from then on, so that the script is read-only. **ACL**: `Atomic` needs the permission to run `EVALSHA` and `EVAL` (or the `_RO` variants), which a user of `+@read` alone does not have; without `Atomic` the user needs `+smismember +exists` only. A swap with `RENAME` never leaves the key missing, so it does not need `Atomic`. The cost is the allocations of a call (fewer with `Atomic`) and some latency: measure it on your own store.

## Failure behaviour

When a call returns an error, the engine keeps the answers (member or not member) that the provider set before it failed and treats the others as unknown; a provider that panics is not trusted at all. The provider and the resolver never apply a failure policy: they leave what they cannot answer unknown and the engine applies `onError` of the segment definition and of the rule.

| Cause | Lookups | Error |
|---|---|---|
| Store down, connection broken | all unknown | `redissegment.ErrUnavailable` |
| The per-call timeout or a network timeout | all unknown | `redissegment.ErrTimeout` |
| The caller's context ended (before or during the call) | the ones answered before keep their answer, the others are unknown | `redissegment.ErrCanceled`, or `resolver.ErrWaiting` when it ended while waiting |
| The store replied with an error (no permission, wrong type, loading) for one command | the lookups of that command are unknown, the others keep their answer | `redissegment.ErrServer` |
| A reply that does not fit the command | the lookups of that command are unknown | `redissegment.ErrProtocol` |
| A reply that cannot be parsed (a malformed integer, a broken frame) on any command of the call, or of the script retry | **every** lookup of the call is unknown, even the ones answered before it | `redissegment.ErrUnavailable` |
| The connection ends mid-call (EOF, reset), or a node has no connection to give (dial error, pool timeout, pool exhausted, closed client), after some replies | the replies that were complete keep their answer, the others are unknown | `redissegment.ErrUnavailable` |
| The breaker of the segment is open | unknown, no call | `resolver.ErrCircuitOpen` |
| Segment not in the options | unknown | none |
| Set key missing (default) | non-members unknown | none |
| The id is longer than `MaxIDBytes` | unknown, not sent | none |
| The context of the caller ended before the call | what the cache has, the rest unknown | `resolver.ErrWaiting` with the context error |
| After `Shutdown` | unknown | `resolver.ErrClosed`, `redissegment.ErrClosed` |

The errors hold a class and counts, never an id, a key or an address: the resolver wraps the error of the store in a `*resolver.StoreError` whose message is fixed (the error of the store stays reachable with `errors.Is` and `errors.As`), and a test checks that no id, key, address or password appears in any error, status or observation. The engine then reports the status `STORE_ERROR` for the evaluation. With the store down, a request takes at most the deadline of its context plus the time to return: with an open breaker it returns without a network call.

## Lifecycle

`segment.Initializer`, `segment.Shutdowner`, `segment.Existencer` and `segment.HealthReporter` are optional interfaces in the `segment` package (the `Provider` interface does not change). `redissegment.Provider` and `resolver.Resolver` implement them, and the relay will reach them with a type assertion.

- `Init` sends one pipeline with the commands that the lookups will use, on the keys that you configured: `SMISMEMBER` and `EXISTS` for a set segment (only `SMISMEMBER` with `MissingKey: notMember`), the script commands for `Atomic` (the `_RO` pair first, then `EVAL` and `EVALSHA`), `EXISTS` for a keys segment. It checks that `PING` answers `PONG` and that each reply has the shape of its command. It fails with `ErrInit`, naming the command that the store refuses, so an old server or a restricted user is found at the start. It probes each distinct namespace once (the key of a set up to its `{tag}`, or the prefix of a keys segment) with the key of its first segment by name, and at most 16 namespaces in all (`MaxInitProbes`, the set and keys layouts together, the first ones by name); the others are not probed. `Provider.Exists` needs `EXISTS` even with `notMember`, and `Init` does not check it in that mode.
- `Shutdown` is safe to call twice. It stops new calls (`Contains`, `Init` and `Exists` return `ErrClosed`, `Contains` with every lookup unknown), waits for the calls in flight until its context ends, then ends them: no call reaches the store after it returns. The resolver waits for the calls in flight, until the context of `Shutdown` ends. The `Shutdown` of the store gets a context that ends `ShutdownGrace` (default 5 seconds) after the deadline of the first `Shutdown` of the resolver, or after the call when that context has no deadline, so a store that stops only on cancellation does not stay pending. The existence check that follows an unanswered lookup runs at most once at a time for a segment: a store that hangs in `Exists` leaves one overdue check for the segment, not one for each lookup.
- `Health()` reports whether the provider is ready, closed, and the class of the last failure. `Exists` tells whether the key of each segment is there.

## Metrics

The relay proxy will export the observations of the resolver (issue 16 and 18): `ObserveCall` for the totals of a call, `ObserveSegment` for each segment of it (the first eight segments of a call), `ObserveBreaker` for a change of state. The names below are the plan, not available yet: labels carry the segment name, never an id.

| Metric | From |
|---|---|
| `semaphore_segment_lookups_total{segment,result}` | `SegmentObservation` (lookups, hits, misses, unknown by reason) |
| `semaphore_segment_lookup_duration_seconds{segment}` | `CallObservation.Duration` |
| `semaphore_segment_cache_total{segment,outcome}` | hits, misses, coalesced |
| `semaphore_segment_unknown_total{segment,reason}` | short circuits and failures by class |
| `semaphore_segment_breaker_state{segment}` | `BreakerEvent` and `Status()` |

## Limits and sizing

- A segment with more than 1000 ids in a call is several `SMISMEMBER` in the same pipeline. With `Atomic` it is one script. A rule looks up one attribute value per term, so calls are small.
- The memory of the cache is in the Cache section above, with the exact and the approximate parts: the bound on entries is exact, the memory is an estimate from measured sizes.
- A lookup that is not cached costs one round trip (tens of microseconds on a local network). With a 30 s TTL, a hot id is asked about twice a minute, however many requests use it.
- The ids that a segment never saw are cached as non members for `NegativeTTL`; a flood of distinct ids fills the LRU and evicts the useful entries, which is what `MaxEntries` bounds.
- Needs `SMISMEMBER` (Valkey 7.2 or Redis 6.2), and `EVALSHA`/`EVAL` for `Atomic`.
- Not done: a cap of entries across all the segments, and freeing the state of a segment that is no longer in the configuration.

## Cluster notes

- `Atomic` works on a cluster because each script reads one key.
- The default set keys have a hash tag for each segment, so the lookups of one segment go to one node, and a temporary key with the same tag can be renamed onto the live key. A temporary key without the tag cannot (`CROSSSLOT`).
- A call over several segments is a pipeline that the client splits by node: it works across slots, with one round trip per node.
- The keys of the `keys` layout spread over the nodes.
- The tests of the package start a real three-node cluster (three nodes in one container, ports mapped by Docker, the client remaps the addresses that the nodes announce) and check these rules, including the `RENAME` swap in one slot.
