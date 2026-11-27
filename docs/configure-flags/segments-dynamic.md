# Segments - dynamic

A dynamic segment is a segment of type `redis` or `valkey`: the members stay in the store and are looked up while a flag is evaluated. See [Segments](segments.md) for what a segment is, the rule syntax and the three answers (member, not member, unknown). For small lists that change rarely use a [static segment](segments-static.md).

- All the lookups of one evaluation go to the store in one batched round trip.
- The resolver in front of the provider adds a bounded cache with a short negative TTL, coalescing of identical lookups and a circuit breaker for each segment. Errors are never cached.
- When the store cannot answer, the lookup is unknown and the failure policy of the segment (`onError`) decides. With the store down, a request takes at most its deadline plus a small margin.
- The provider only reads, so a read-only ACL user is enough.

## Which type to choose

| | `redis` ([page](segments-redis.md)) | `valkey` ([page](segments-valkey.md)) |
|---|---|---|
| Package | `redissegment` | `valkeysegment` |
| Client | go-redis v9 | valkey-go |
| One call | one explicit pipeline | one `DoMulti`; the client batches concurrent calls on a connection (automatic pipelining) |
| `Atomic` script mode | yes | no (no script is sent) |
| Throughput under 64 to 256 concurrent callers (measured, see the Valkey page) | about 47000 calls/s | about 73000 to 77000 calls/s with `pipelining: always` or `auto` |
| A reply nobody asked for (broken server or proxy) | the connection is dropped, the call returns an error | valkey-go panics in its reader: the process ends |
| Client settings | go-redis options | `pipelining` (`always`, `auto`), `blockingPipeline`, `retry` |
| Server | Redis 6.2+, Valkey 7.2+ | Redis 6.2+, Valkey 7.2+ |

- Pick `valkey` for a Valkey store with many concurrent calls: valkey-go merges their writes.
- Pick `redis` when the store sits behind a proxy you do not trust, when you need `Atomic` (a set swapped by deleting the key), or run behind a go-redis failover or ring setup.
- Both have the same data model, layouts (`set` and `keys`), answers and failure classes, are read-only and pass the same conformance suite on the same resolver.

## Pages

- [Segments - dynamic: Redis](segments-redis.md): the data model, loading members, the resolver, metrics and sizing (shared by both types).
- [Segments - dynamic: Valkey](segments-valkey.md): the valkey-go provider.
