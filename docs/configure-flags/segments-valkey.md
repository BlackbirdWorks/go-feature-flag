# Segments in Valkey (valkey-go)

`modules/core/segment/valkeysegment` is a `segment.Provider` for the `valkey` segment type. It reads the members from Valkey (or Redis 6.2 or later) with [valkey-go](https://github.com/valkey-io/valkey-go). It has the same data model, answers and failure classes as the `redis` provider: see [Segments in Redis](segments-redis.md) for the layouts, how to load members, the missing key rule, the ACL and the resolver.

## Which type to choose

| | `redis` ([page](segments-redis.md)) | `valkey` (this page) |
|---|---|---|
| Package | `redissegment` | `valkeysegment` |
| Client | go-redis v9 | valkey-go |
| One call | one explicit pipeline | one `DoMulti`; the client batches concurrent calls on a connection (automatic pipelining) |
| `Atomic` script mode | yes | no (no script is sent) |
| Server | Redis 6.2+, Valkey 7.2+ | Redis 6.2+, Valkey 7.2+ |

- Pick `valkey` for a Valkey store with many concurrent calls: valkey-go merges their writes.
- Pick `redis` when you need `Atomic` (a set swapped by deleting the key), or run behind a go-redis failover or ring setup.
- Both are read-only and pass the same conformance suite (`segmenttest`) on the same resolver.

## Use it

```go
store, err := valkeysegment.Dial(&valkey.ClientOption{InitAddress: []string{"valkey:6379"}}, &valkeysegment.Options{
    Segments: map[string]valkeysegment.SegmentOptions{
        "beta":    {},                                                // set at semaphore:segment:{beta}
        "flagged": {Layout: valkeysegment.LayoutKeys, Key: "flag:"}, // keys flag:<id>
    },
    Timeout: 50 * time.Millisecond, // per call, under the context of the caller
})
// wrap it in the resolver as for the redis type
```

- `Dial` creates the client, owns it, and `Shutdown` closes it. `New(client, opts)` takes a `valkey.Client` that you own.
- `Dial` forces `DisableCache` (no client-side caching: the resolver caches) and `DisableRetry` (a failed read is not sent again behind the call timeout), whatever you set.
- A cluster is detected by the client from `InitAddress`.
- `Options` are the ones of the `redis` type without `Atomic`: `Segments`, `KeyPrefix`, `MissingKey`, `MaxIDBytes`, `Timeout` (default 100 ms) and `Observer`. The observer and the counters are the types of `redissegment`.

## One round trip per call

- All the lookups of a call are one `DoMulti`: one write to the connection and the replies read in order. With a cluster it is one batch per node.
- A set segment is `SMISMEMBER` plus an `EXISTS` of its key (skipped with `MissingNotMember`); a keys segment is one `EXISTS` per id. More than 1000 ids of one set are several `SMISMEMBER` in the same `DoMulti`.
- The tests prove it by counting the writes of the client connection and the `calls` of `INFO commandstats` on a real store.

## Failures and shutdown

- A transport error keeps the answers that were read (a healthy cluster node keeps its answers); the rest stay unknown with class `unavailable`, `timeout` or `canceled`.
- An error reply (`WRONGTYPE`, `NOPERM`) is class `server`. A reply that is not the one the command returns, or a stream that does not parse, is class `protocol` and nothing of the call is kept.
- The call ends at its `Timeout` at the latest. valkey-go watches the deadline of a call that is alone on its connection and not its cancel, so a cancel by the caller is reported as `canceled` but may wait for the timeout.
- `Shutdown` stops new calls, waits for the calls in flight until its context ends, then closes an owned client: that ends a call still in flight. It is safe to call twice.
- Errors, status and metrics hold classes and counts only: never an id, a key, an address or a password.

## Limits

- Needs `SMISMEMBER` (Valkey 7.2 or Redis 6.2). `Init` checks the commands that your layouts use.
- The relay proxy does not use this provider yet (issue 16).

## Benchmark

On a commodity 4-core Linux machine against one Valkey 8 node in a container, one call of 1, 10 and 100 ids of a set segment costs about the same with both providers (about 40 to 50 microseconds at 1 and 10 ids, about 80 microseconds at 100 ids, half of that per call with parallel callers). valkey-go allocates fewer objects but more bytes per call for large calls. Measure on your own store: `go test -tags=docker,bench -run '^$' -bench Contains ./modules/core/segment/valkeysegment`.
