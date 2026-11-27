# Segments in Valkey

The Go package `modules/core/segment/valkeysegment` is a `segment.Provider` that reads segment members from Valkey (or Redis 6.2 or later; any store that has `SMISMEMBER`). It only reads: a read-only ACL user is enough. See [Segments](segments.md) for the rule syntax and the three answers (member, not member, unknown).

The relay proxy does not use it yet: the `segments` setting and the connection wiring come later (issue 16). Until then the package is for the Go module.

## Data model

A segment is either a set (default) or one key per member.

| Layout | Key | Lookup |
|---|---|---|
| `set` | `<prefix>{<name>}` (the default) or the key you set | `SMISMEMBER key id...` |
| `keys` | `<prefix><name>:<id>` (the default) or `<your prefix><id>` | `EXISTS key` for each id |

- The default prefix is `semaphore:segment:`. The `{name}` is a hash tag: a temporary key with the same tag (`semaphore:segment:{beta}:tmp`) is in the same cluster slot, so `RENAME` works.
- A segment that is not in the provider options is never looked up (its lookups stay unknown). A flag cannot choose a key.
- With the `keys` layout an empty id is not looked up (it stays unknown): the key would be the bare prefix.
- Members are compared byte for byte. The provider does not normalise or hash ids; do it before you load them and in the rule's attribute.

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

`RENAME` is atomic: a reader sees the old set or the new one, never a missing key. The `semaphore-cli segment` tooling (issue 17) will do this for you.

## Use it

```go
provider, err := valkeysegment.New(redisClient, valkeysegment.Options{ // a redis.UniversalClient
    Segments: map[string]valkeysegment.SegmentOptions{
        "beta":    {},                                                   // set at semaphore:segment:{beta}
        "flagged": {Layout: valkeysegment.LayoutKeys, Key: "flag:"},     // keys flag:<id>
    },
    Timeout: 50 * time.Millisecond, // per call, under the context of the caller
})
// ...
client, err := ffclient.New(&ffclient.Config{ /* ... */ Segments: catalog, SegmentProvider: provider })
```

- `New` takes any go-redis client (a single node, a failover set or a cluster), so the relay can share its connection. `Dial` builds the client from `redis.UniversalOptions` and `Close` closes it.
- Options: `Segments`, `KeyPrefix`, `MissingKey`, `Timeout` (default 100 ms), `Observer`.

## One round trip

All the lookups of a call go out in one pipeline, grouped by segment: one `SMISMEMBER` for all the ids of a set segment, one `EXISTS` per id of a keys segment. A rule makes one call, so a rule is one round trip on a single node. On a cluster the client splits a pipeline by node, so it is one round trip per node.

## Missing key

A set that does not exist is not the same as an empty segment, and Valkey deletes a set that becomes empty. The provider therefore adds an `EXISTS` of the set key to the same pipeline:

| `MissingKey` | A non-member of a set whose key does not exist is |
|---|---|
| `unknown` (default) | unknown: the segment is not loaded, and the engine applies the failure policy |
| `notMember` | not a member, and the `EXISTS` is not sent |

A member is always a member. The `keys` layout cannot tell a missing member from a segment that is not loaded: a missing key is not a member.

## Failure behaviour

The provider never applies a failure policy: it leaves what it cannot answer unknown and the engine applies `onError` of the segment definition and of the rule.

| Cause | Lookups | Error (`*valkeysegment.LookupError`) |
|---|---|---|
| Store down, connection broken | all unknown | `ErrUnavailable` |
| The per-call timeout or a network timeout | all unknown | `ErrTimeout` |
| The caller's context ended (before or during the call) | all unknown | `ErrCanceled` |
| The store replied with an error (no permission, wrong type, loading) for one command | the lookups of that command are unknown, the others keep their answer | `ErrServer` |
| A reply that does not fit the command | the lookups of that command are unknown | `ErrProtocol` |
| Segment not in the options | unknown | none |
| Set key missing (default) | non-members unknown | none |

The error holds the class and counts, never an id, a key or an address. The engine then reports the status `STORE_ERROR` for the evaluation.

## Observability

`Options.Observer` receives one `Observation` per call: lookups, answered, unknown, round trips, duration and failure class. `valkeysegment.Counters` is a ready-made observer with a `Snapshot`. The relay will export them as metrics without ids as labels.

## Limits and notes

- Not yet in this package: the in-process cache with TTL, singleflight and the circuit breaker of the issue; they will wrap the provider.
- The cost of a call is the number of ids: `SMISMEMBER` takes them all as arguments. Rules look up one attribute value per term, so calls are small.
- Needs `SMISMEMBER`: Valkey 7.2 or Redis 6.2.
- Cluster: the default set keys have a hash tag per segment, so the lookups of one segment go to one node. Keys of the `keys` layout spread over the nodes.
