# Segments - dynamic

A dynamic segment is a segment of type `redis` or `valkey`: the members stay in the store and are looked up while a flag is evaluated. See [Segments](segments.md) for what a segment is, the rule syntax and the three answers (member, not member, unknown). For small lists that change rarely use a [static segment](segments-static.md).

- All the lookups of one rule go to the store in one batched call (`resolveSegments` batches the terms of a rule, not of a whole flag evaluation): one round trip to a single node, one batch for each node on a cluster. A flag evaluation with several segment rules can issue several calls.
- The providers work alone, but then every lookup goes to the store. When the resolver is used, it adds a bounded cache with a short negative TTL, coalescing of identical lookups and a circuit breaker for each segment. Errors are never cached.
- When the store cannot answer, the lookup is unknown and the failure policy of the segment (`onError`) decides. With the store down, a request takes at most its deadline plus a small margin. Exception: for a `valkey` segment whose peer stops reading until the connection ring is full, the wait can last until the `ConnWriteTimeout` of the client closes the connection (about 10 seconds by default), see [Limits](segments-valkey.md#limits).
- The provider only reads, so a read-only ACL user is enough.

## Which type to choose

| | `redis` ([page](segments-redis.md)) | `valkey` ([page](segments-valkey.md)) |
|---|---|---|
| Package | `redissegment` | `valkeysegment` |
| Client | go-redis v9 | valkey-go |
| One call (one lookup call, single node) | one explicit pipeline | one `DoMulti`; the client batches concurrent calls on a connection (automatic pipelining) |
| `Atomic` script mode | yes | no (no script is sent) |
| Throughput under 64 to 256 concurrent callers (measured, default client settings, see the Valkey page) | about 32000 to 33000 calls/s | about 51000 to 54000 calls/s with `pipelining: always` or `auto` |
| One reply of a batch has the wrong shape (not an integer, wrong length) | the lookups of that command are unknown, the other commands keep their answers (a stream that cannot be parsed discards the whole call) | every lookup of the call is unknown |
| A reply nobody asked for (broken server or proxy) | the connection is dropped, the call returns an error | valkey-go panics in its reader: the process ends |
| Client settings | go-redis options | `pipelining` (`always`, `auto`), `blockingPipeline`, `retry` |
| Server | Redis 6.2+, Valkey 7.2+ | Redis 6.2+, Valkey 7.2+ |

- Pick `valkey` for a Valkey store with many concurrent calls: valkey-go merges their writes.
- Pick `redis` when the store sits behind a proxy you do not trust, when you need `Atomic` (a set swapped by deleting the key), or run behind a go-redis failover or ring setup.
- Both have the same data model, layouts (`set` and `keys`), answers and failure classes, are read-only and pass the same conformance suite on the same resolver.

## Configure in the relay

The stores are `redis` and `valkey` [connections](../relay-proxy/bootstrap-config.md#connections-name) of the bootstrap file, because the clients own their connections and caching. The resolver settings are its `segments.resolver` section (see [its keys](../relay-proxy/bootstrap-config.md#segments)). The options of the providers are keys of the connection with the same names.

- The relay checks the connections and the section at the start, and `semaphore-cli config validate` checks them too.
- The password is a `fromEnv` or `fromFile` reference. It is never printed.
- The segment definitions are published with the flags, like any other flag content. That comes with the next step of the segments work; the bootstrap file has no `segments.definitions`.
- Today the relay only validates the connections and the section and logs that the stores are configured but not active.

<!-- full-example -->
```yaml
version: 2
connections:
  control: {type: s3, region: us-east-1, bucket: acme-control}
  segments-valkey:
    type: valkey
    addresses: ["valkey-1:6379", "valkey-2:6379"]
    username: semaphore
    password: {fromEnv: VALKEY_PASSWORD}
    tls: {enable: true, caBundleFile: /etc/ssl/valkey-ca.pem}
    pipelining: auto
    lookupTimeout: 100ms
controlPlane:
  connection: control
  manifest: {key: manifests/stable.yaml}
segments:
  resolver:
    callTimeout: 2s
    default: {ttl: 30s, negativeTtl: 5s, breakerFailures: 5}
```

## Pages

- [Segments - dynamic: Redis](segments-redis.md): the data model, loading members, the resolver, metrics and sizing (shared by both types).
- [Segments - dynamic: Valkey](segments-valkey.md): the valkey-go provider.
