# Segments - dynamic: Valkey

`modules/core/segment/valkeysegment` is a `segment.Provider` for the `valkey` segment type (a dynamic segment). It reads the members from Valkey (or Redis 6.2 or later) with [valkey-go](https://github.com/valkey-io/valkey-go). It has the same data model, answers and failure classes as the `redis` provider: see [Segments - dynamic: Redis](segments-redis.md) for the layouts, how to load members, the missing key rule, the ACL and the resolver.

## Which type to choose

See [Segments - dynamic](segments-dynamic.md) for the comparison of the `redis` and `valkey` types. In short: `valkey` batches concurrent calls on a connection; `redis` has the `Atomic` script mode.

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
- `Dial` builds the client from your `valkey.ClientOption` and the client settings below; the settings override the same fields of the option.
- A cluster is detected by the client from `InitAddress`.
- `Options` are the ones of the `redis` type without `Atomic`: `Segments`, `KeyPrefix`, `MissingKey`, `MaxIDBytes`, `Timeout` (default 100 ms) and `Observer`. The observer and the counters are the types of `redissegment`.

## Client settings

These options only apply to a client that `Dial` creates. The names are the ones of the relay proxy `segments` configuration. The defaults are the safe ones.

| Option | Values (default first) | What it trades |
|---|---|---|
| `pipelining` | `always`, `auto` | `always`: the client reads in the background even for a lone call; a disconnect inside a batch keeps the replies already read and a cancel ends the call at once. `auto` (the valkey-go default): a lone call is written and read synchronously and the background reader starts under concurrent load; a transport failure inside a batch loses the whole batch (all unknown) and a cancel is seen only at the deadline: a reply that arrives after the cancel and before the deadline is returned as the answer (a correct one). |
| `blockingPipeline` | `0` (never), or a number of commands | From this many commands in one call valkey-go moves the call to a dedicated connection, where a cancel waits for the connection to close. `0` keeps every call on the shared connection. |
| `retry` | `false`, `true` | `true` lets valkey-go send a failed read again behind the call timeout, so a lookup may be sent twice. |

Client-side caching is not used: lookups go through `DoMulti`, which valkey-go never caches, and the resolver caches. One enum (`pipelining`) is used instead of the two valkey-go booleans (`AlwaysPipelining`, `DisableAutoPipelining`) because the booleans can express a combination that valkey-go resets. valkey-go also has a mode with one pooled connection per call; it is not offered: it is slower and loses its connection cap when a call is cancelled.

## One round trip per call

- All the lookups of a call are one `DoMulti`: one write to the connection and the replies read in order. With a cluster it is one batch per node.
- A set segment is `SMISMEMBER` plus an `EXISTS` of its key (skipped with `MissingNotMember`); a keys segment is one `EXISTS` per id. More than 1000 ids of one set are several `SMISMEMBER` in the same `DoMulti`.
- The tests prove it by counting the writes of the client connection and the `calls` of `INFO commandstats` on a real store.

## Failures and shutdown

- A transport error keeps the answers that were read (a healthy cluster node keeps its answers); the rest stay unknown with class `unavailable`, `timeout` or `canceled`.
- An error reply (`WRONGTYPE`, `NOPERM`) is class `server`. A reply that is not the one the command returns, or a stream that does not parse, is class `protocol` and nothing of the call is kept.
- The call ends at its `Timeout` at the latest. With `pipelining: always` (default) a cancel by the caller also ends it at once and a disconnect inside a batch keeps the replies already read; with `auto` and `off` it does not (see Client settings). A client you pass to `New` should be created with `AlwaysPipelining`.
- `Shutdown` stops new calls, waits for the calls in flight until its context ends, then closes an owned client: that ends a call still in flight. It is safe to call twice.
- Errors, status and metrics hold classes and counts only: never an id, a key, an address or a password.

## Limits

- **Unsolicited reply:** a server or proxy that sends a reply nobody asked for makes valkey-go panic in its reader goroutine, which ends the process. The reader runs on every connection that pipelines in the background: all of them with `pipelining: always`, and with `auto` the connections that concurrent calls share. The `redis` type (go-redis) drops such a connection and returns an error: use it when the store sits behind a proxy that you do not trust.
- **Full ring:** when a slow peer stops reading until the ring of a connection is full (1024 calls waiting), valkey-go waits for a slot without looking at the context, so the call timeout does not end the wait. It ends when the connection is closed because of the `ConnWriteTimeout` of the client option (about 10 seconds by default): set a lower value if you need a tighter bound.
- **Set created during a call:** a set segment asks `EXISTS` of its key before `SMISMEMBER`, so a set created while the call runs is unknown or answered by `SMISMEMBER`, never a non-member that matches no state of the set.
- Needs `SMISMEMBER` (Valkey 7.2 or Redis 6.2). `Init` checks the commands that your layouts use.
- The relay proxy does not use this provider yet (issue 16).

## Throughput

`TestE2ELoad` (docker tag) drives each provider with 64 and 256 goroutines, batches of 1, 10 and 100 ids over both layouts, and checks every answer against the seeded members. It also runs `CLIENT KILL` of every connection and `CLIENT PAUSE` so that deadlines fire, and checks that there is no wrong answer, no call past its deadline plus a second, no goroutine left, and that the same provider answers correctly again.

Measured on a commodity 4-core Linux machine against one Valkey 8 container, 12000 calls per run, race detector off:

| Provider | calls/s, 64 goroutines | p50 / p99, 64 | calls/s, 256 goroutines | p50 / p99, 256 | allocations per call |
|---|---|---|---|---|---|
| `redis` | 46700 | 1.3 / 2.6 ms | 47500 | 6.2 / 8.2 ms | 166 |
| `valkey`, `always` | 73400 | 0.8 / 1.9 ms | 77000 | 3.2 / 5.9 ms | 128 |
| `valkey`, `auto` | 72600 | 0.8 / 2.0 ms | 77100 | 3.0 / 6.6 ms | 128 |

The load generator shares the machine with the store, so read the numbers as a comparison. Under a fault no run had a wrong answer; failed calls had the classes `unavailable` (kill) or `timeout` (pause). With a single caller one call costs about 45 to 55 microseconds with all providers. Run the benchmark with `go test -tags=docker,bench -run '^$' -bench Contains -cpu 1,4,8 ./modules/core/segment/valkeysegment`.
