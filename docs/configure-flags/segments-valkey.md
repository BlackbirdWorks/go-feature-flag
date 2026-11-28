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

- `Dial` creates the client, owns it, and `Shutdown` closes it. `New(client, opts)` takes a client that you own. `NewOwning(client, opts)` takes a client that the provider closes, as `Dial` does.
- `Dial` builds the client from your `valkey.ClientOption` and the client settings below; the settings override the same fields of the option.
- A cluster is detected by the client from `InitAddress`.
- `Options` are the ones of the `redis` type: `Segments`, `KeyPrefix`, `MissingKey`, `MaxIDBytes`, `Timeout` (default 100 ms) and `Observer`. `Atomic` (`*bool`) is on by default and is turned off with `Atomic: &false` (see [Atomic existence check](#atomic-existence-check)); `Logger` receives the INFO line of the replica default; the provider sends no script. The observer and the counters are the types of `redissegment`.

## Client settings

These options only apply to a client that `Dial` creates. The names are the ones of the relay proxy `segments` configuration. The defaults are the safe ones.

| Option | Values (default first) | What it trades |
|---|---|---|
| `pipelining` | `always`, `auto` | `always`: the client reads in the background even for a lone call; a disconnect inside a batch keeps the replies already read and a cancel ends the call at once. `auto` (the valkey-go default): a lone call is written and read synchronously and the background reader starts under concurrent load; a transport failure inside a batch loses the whole batch (all unknown) and a cancel is seen only at the deadline: a reply that arrives after the cancel and before the deadline is returned as the answer (a correct one). |
| `blockingPipeline` | `0` (never), or a number of commands | From this many commands in one call valkey-go moves the call to a dedicated connection, where a cancel waits for the connection to close. `0` keeps every call on the shared connection. |
| `retry` | `false`, `true` | `true` lets valkey-go send a failed read again behind the call timeout, so a lookup may be sent twice. |
| `ConnWriteTimeout` (relay proxy: `timeouts.io`) | unset, or 10 ms to 1 min | The read and write timeout of each connection (valkey-go `ConnWriteTimeout`). It also ends the wait of a call behind a peer that stopped reading until the ring is full (see [Limits](#limits)). Unset: the value of your client option, else ten call timeouts and at least 1 s (`DefaultConnWriteTimeout`). A value below the call timeout closes connections that are only slow: the relay proxy refuses `timeouts.io` below `lookupTimeout`, the Go option is not checked against `Timeout`. A small value also raises the rate of the background `PING` of valkey-go (it is a fraction of this timeout). |

Client-side caching is not used: lookups go through `DoMulti`, which valkey-go never caches, and the resolver caches. One enum (`pipelining`) is used instead of the two valkey-go booleans (`AlwaysPipelining`, `DisableAutoPipelining`) because the booleans can express a combination that valkey-go resets. valkey-go also has a mode with one pooled connection per call; it is not offered: it is slower and loses its connection cap when a call is cancelled.

## Atomic existence check

With `missingKey: unknown` a set segment needs two answers: does the key exist, and which ids are members. They are asked in one transaction, on by default:

- A set segment is `MULTI`, `EXISTS`, `SMISMEMBER`, `EXEC`, in the same `DoMulti` as the rest of the call (still one write on a single node). The server runs the two commands with nothing in between, so a set that is removed or expires during the call is "missing" for both: the lookups stay unknown (the `missingKey` rule), never a non-member of a set that was there.
- Off (`atomic: false` in the relay proxy, `Atomic: &false` in Go): `EXISTS` then `SMISMEMBER` outside a transaction. A set that disappears between them answers non-member for a former member, and the resolver may cache it.
- With `missingKey: notMember` (the default) no existence check is sent, so there is nothing to make atomic: a vanished set is a non-member by that rule.
- Cost: 4 commands for a set instead of 2 (a keys segment is one `EXISTS` per id either way). Measured on one Valkey node, one caller: about 10 to 20 percent more time for a call, and three more allocations (1, 10 and 100 ids in one set).
- Cluster: `EXISTS` and `SMISMEMBER` name the same key, so they hash to one slot and the transaction runs on one node. valkey-go refuses a `DoMulti` that mixes a transaction with commands of other slots, so on a cluster client each transaction is sent in its own `DoMulti`, in parallel with the others and with the rest of the call.
- A transaction is not a read: it goes to the primary and never to a replica, so `SendToReplicas` has no effect on set segments with the check on. Turn the check off to read sets from replicas.
- Replica reads: a `DoMulti` with `MULTI` always goes to the primary. When `Dial` sees replica reads (`SendToReplicas` or `ReplicaOnly`) and `Atomic` is not set, the check is off and one INFO line says so, so the set lookups keep going to the replicas. An explicit `Atomic: true` wins, and those lookups go to the primary. `New` cannot see the replica reads of its client and defaults to on.

| Client | `Atomic` unset | `Atomic: true` | `Atomic: false` |
|---|---|---|---|
| Standalone | on | on | off |
| Cluster | on | on | off |
| Cluster with replica reads | off (INFO line) | on, lookups go to the primary | off |
| Standalone with replica reads (`SendToReplicas`; valkey-go refuses `ReplicaOnly` for a standalone client) | off (INFO line) | on, lookups go to the primary | off |

- A client wrapper that hides `Mode()` around a cluster client is not supported: the provider then sends every transaction in one `DoMulti` with commands of other slots, and valkey-go refuses it.
- The `redis` type closes the same gap with a Lua script (`atomic: true`, off by default).

## One round trip per call

- All the lookups of a call are one `DoMulti`: one write to the connection and the replies read in order. With a cluster it is one batch per node.
- A set segment is `SMISMEMBER` plus an `EXISTS` of its key (skipped with `MissingNotMember`; in one transaction by default); a keys segment is one `EXISTS` per id. More than 1000 ids of one set are several `SMISMEMBER` in the same `DoMulti`.
- The tests prove it by counting the writes of the client connection and the `calls` of `INFO commandstats` on a real store.

## Failures and shutdown

- A transport error keeps the answers that were read (a healthy cluster node keeps its answers); the rest stay unknown with class `unavailable`, `timeout` or `canceled`.
- An error reply (`WRONGTYPE`, `NOPERM`) is class `server` and fails only its command. A reply that is not the one the command returns (not an integer 0 or 1, a list of the wrong length or with a null, a bulk string instead of an integer), or a stream that does not parse, is class `protocol` (an `EXEC` that is nil, a wrong length, a null or a string) and nothing of the call is kept: every lookup of the call is unknown. The `redis` type keeps the answers of the other commands when one reply has the wrong shape (see [Which type to choose](segments-dynamic.md#which-type-to-choose)).
- The call ends at its `Timeout` at the latest, with one exception: when a slow peer stops reading until the ring of a connection is full, the wait for a ring slot ignores the deadline (see [Limits](#limits)). With `pipelining: always` (default) a cancel by the caller also ends the call at once and a disconnect inside a batch keeps the replies already read; with `auto` it does not (see Client settings). A client you pass to `New` should be created with `AlwaysPipelining`.
- `Shutdown` stops new calls, waits for the calls in flight until its context ends, then closes an owned client in the background: that ends a call still in flight. It returns the error of its context when the context ends first, in the wait or in the close, and a close that hangs on a silent peer ends later by itself. It is safe to call twice. `Close` waits for the close.
- Errors, status and metrics hold classes and counts only: never an id, a key, an address or a password.

## Limits

- **Unsolicited reply:** a server or proxy that sends a reply nobody asked for makes valkey-go panic in its reader goroutine, which ends the process. The reader runs on every connection that pipelines in the background: all of them with `pipelining: always`, and with `auto` the connections that concurrent calls share. The `redis` type (go-redis) drops such a connection and returns an error: use it when the store sits behind a proxy that you do not trust.
- **RESP set for an array:** valkey-go has no public way to tell a RESP3 set from an array (`IsArray` is true for both), so a set reply of the right length with 0 and 1 values is read as the array it stands for. A wrong length or value is still a protocol error.
- **Close that never returns:** `Shutdown` returns at its deadline, but a client close that never returns leaves one goroutine until it does.
- **Full ring:** when a slow peer stops reading until the ring of a connection is full (1024 calls waiting), valkey-go waits for a slot without looking at the context, so the call timeout does not end the wait. It ends when the connection is closed because of the `ConnWriteTimeout` of the client option (about 10 seconds by default): set a lower value if you need a tighter bound.
- **Set created or removed during a call:** with the atomic check (default) both commands see one state of the set. Without it, `EXISTS` runs before `SMISMEMBER`: a set created in between is unknown or answered by `SMISMEMBER`, and a set removed in between answers non-member (see [Atomic existence check](#atomic-existence-check)).
- Needs `SMISMEMBER` (Valkey 7.2 or Redis 6.2). `Init` checks the commands that your layouts use.
- The relay proxy does not use this provider yet (issue 16).

## Throughput

`TestE2ELoad` (docker tag) drives each provider with 64 and 256 goroutines and exactly 12000 calls, batches of 1, 10 and 100 ids over both layouts, and checks every answer against the seeded members. It also runs `CLIENT KILL` of every connection and `CLIENT PAUSE` so that deadlines fire, and checks that there is no wrong answer, that no call goes past its deadline by more than a tight bound, that deadlines do fire under the pause, that no goroutine is left, and that the same provider answers correctly again.

Measured on a commodity 4-core Linux machine against one Valkey 8 container, race detector off, both providers with the default connection and pool settings of their clients, median of 3 runs. The machine was also busy with other work (load average above 3), so read the numbers as a comparison, not as absolute figures:

| Provider | calls/s, 64 goroutines | p50 / p99, 64 | calls/s, 256 goroutines | p50 / p99, 256 | allocations per call |
|---|---|---|---|---|---|
| `redis` | 32300 | 1.9 / 4.4 ms | 33100 | 9.0 / 12.2 ms | 166 |
| `valkey`, `always` | 54100 | 1.1 / 2.8 ms | 52700 | 4.6 / 10.4 ms | 129 |
| `valkey`, `auto` | 51700 | 1.1 / 3.0 ms | 51300 | 4.6 / 9.5 ms | 130 |

Under a fault no run had a wrong answer; failed calls had the classes `unavailable` (kill) or `timeout` (pause).

The benchmark (`go test -tags=docker,bench -run '^$' -bench Contains -cpu 1,4,8 ./modules/core/segment/valkeysegment`) measures one call of 1, 10 and 100 ids. With one caller the three providers are within about 10 percent of each other (about 55 to 65 microseconds for 1 and 10 ids, about 95 to 115 microseconds for 100 ids). With parallel callers a call costs about 25 microseconds at `-cpu 4` and 16 to 20 microseconds at `-cpu 8` for 1 and 10 ids, again within about 20 percent. `valkey` allocates fewer objects per call (about 18 to 26 against 24 to 123 for `redis`).
