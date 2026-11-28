# Segments - dynamic

A dynamic segment is a segment of type `redis` or `valkey`: the members stay in the store and are looked up while a flag is evaluated. See [Segments](segments.md) for what a segment is, the rule syntax and the two answers (member, not a member) and the opt-in for unknown. For small lists that change rarely use a [static segment](segments-static.md).

- All the lookups of one rule go to the store in one batched call (`resolveSegments` batches the terms of a rule, not of a whole flag evaluation): one round trip to a single node, one batch for each node on a cluster. A flag evaluation with several segment rules can issue several calls.
- The providers work alone, but then every lookup goes to the store. When the resolver is used, it adds a bounded cache with a short negative TTL, coalescing of identical lookups and a circuit breaker for each segment. Errors are never cached.
- When the store cannot answer a lookup, the lookup is not a member by default, and the failure policy of the segment (`onError`) can change that (`unknown` is the opt-in of the three-valued logic). The evaluation status is `STORE_ERROR` for the lookups that were not answered. A snapshot that was loaded and then failed to refresh is not an unanswered lookup: it keeps answering from the last known good set and the status is `STALE`. With the store down, a request takes at most its deadline plus a small margin. Exception: for a `valkey` segment whose peer stops reading until the connection ring is full, the wait can last until the `ConnWriteTimeout` of the client closes the connection (about 10 seconds by default), see [Limits](segments-valkey.md#limits).
- The provider only reads, so a read-only ACL user is enough.

## Modes

> Today the relay reads and checks the `mode` and the `version` of a definition when it builds a generation (see [Checks when a generation is built](#checks-when-a-generation-is-built)), but it does not open the stores yet: a dynamic segment is defined, answers "not a member" and the evaluation reports `NOT_CONFIGURED`. The providers, the snapshots and their refresh come with the next step of [#16](https://github.com/BlackbirdWorks/semaphore/issues/16). The modes are interfaces and reference implementations in `modules/core/segment`; the Redis and Valkey stores keep the `lookup` behaviour described above.

A segment that lives in a store has a mode. A definition with no `mode` is a `snapshot` segment.

| | `snapshot` (default) | `lookup` |
|---|---|---|
| Where the members live | in relay memory, one immutable set, pulled from the store like a retriever | in the store; the relay keeps a bounded LRU of answers |
| Cost per evaluation | a hash lookup, no network, no allocation | a cache hit costs the same; a miss is one batched network call |
| Memory | the whole set (guarded by a maximum of members and bytes) | the LRU only (the least of the two modes) |
| Freshness | as of the last pull; the relay polls the version and pulls again only when it changed | as of the version of the cached entry; entries of another version are misses, and the time-to-live is the fallback |
| Change detection | version token, compared for equality | the same version token, learned off the request path |
| Store down or an error (timeout, broken stream, over the memory guard, malformed reply) | the last known good set keeps answering; the failure class is reported, never a member id | cached answers keep answering until they expire; a miss is not a member, or unknown with the `onError: unknown` opt-in |
| Segment missing in the store (the store answered) | the old set is dropped and nobody is in the segment (not a member); it is served again when the segment comes back. With `missingKey: unknown` the lookups are unknown instead | not a member, or unknown with `missingKey: unknown` |

- A segment that does not exist in the store has no member: the default `missingKey` is `notMember`, for both modes. `missingKey: unknown` is an opt-in that sends the lookups to the failure policy (`onError`). Consequence: an expired or deleted set means "nobody is in the segment". Replace a segment atomically (build the new set under another key and rename it over the old one), not by deleting it and adding the members again. Last known good is for errors only, never for a segment that is gone.
- The `keys` layout is lookup-only. A definition with the `keys` layout and the `snapshot` mode (the default) is invalid and `segment.Definition.Validate` rejects it, naming the segment, the layout and the mode: its members cannot be listed without scanning the whole keyspace.
- The Redis and Valkey providers follow the same default (`MissingKey` is `notMember`); `unknown` is their opt-in. For the snapshot, a set with no member is reported as missing and the holder applies the policy.
- Redis and Valkey version key (`versionKey` of a segment): the snapshot reads it with `GET` and pulls only when its value changed; a version key that does not exist in the store is the fixed token `absent`, which is only equal to itself, so the writer must set the key whenever it changes or deletes the set. A segment with no `versionKey` has no version: the snapshot pulls the whole set on every refresh, which costs a full read for each poll.
- Snapshot reads go to the primary. The Redis provider reads the version key and every page of a scan from the primary of the slot of the key (`ClusterClient.MasterForKey`), whatever the read routing of the client (`RouteRandomly`, `RouteByLatency`, `ReadOnly`): a scan cursor belongs to one node. The Valkey provider never lets `SendToReplicas` send `SSCAN` or `GET` to a replica; do not use `ReplicaOnly` with a snapshot segment. This is proven by unit tests with a fake cluster; there is no replica in the docker tests.
- A page of a scan that is not well formed (a cursor that is not a number, members that are not strings, a cursor that comes back, more than `MaxScanPages` pages) is a protocol error: the holder keeps the last known good set.
- A version key is read as `v:<value>`, and a version key that does not exist is `absent`, so a stored value can never be taken for a missing key.
- ACL: the snapshot mode needs `SSCAN` for the set, and `GET` for a version key, on top of the lookup commands. Set `Snapshot: true` on the segment and `Init` probes both at the start, so a missing permission is an `Init` error that names the command.
- A store kind implements one interface per mode: `Provider` for `lookup`, `SnapshotSource` (a version and a streamed read of the members) for `snapshot`. `segment.RequireMode` is the check for a segment whose store cannot serve its mode; its error names the segment, the mode and the store. The relay runs it when it builds a generation.
- A version is an opaque string. It comes from the definition (`version.value`, bumped by the publisher), from a key in the store that the writer bumps (`version.key`, polled every `version.interval`), or not at all (time-to-live only). The library models these fields and validates them (`value` and `key` are exclusive, `interval` needs a `key`, 100ms to 24h).
- A store that can push changes also implements `ChangeNotifier`; polling the version stays the baseline.
- Nothing in the library starts a goroutine or a timer for this: the relay calls `Holder.Refresh` (snapshot) and `Resolver.RefreshVersions` (lookup) on its own schedule.

## Published form

In a flag set published through the control plane, a dynamic segment is a definition with a `connection` (a connection
of the relay bootstrap file) in a document of kind `Segments`; the flag set lists the document in `segmentSources`.
See [the document format](../go-module/controlplane.md#segments-document).

A `snapshot` segment (the default mode) with a version key that the writer of the members bumps:

<!-- segments-doc -->
```yaml
apiVersion: semaphore/v1
kind: Segments
segments:
  - name: churned
    connection: audiences
    key: semaphore:segment:{churned}
    version:
      key: semaphore:segment:churned:version
      interval: 5s
```

A `lookup` segment with a cache and the `keys` layout (the `keys` layout is lookup only):

<!-- segments-doc -->
```yaml
apiVersion: semaphore/v1
kind: Segments
segments:
  - name: staff
    connection: audiences
    mode: lookup
    layout: keys
    key: "staff:"
    version:
      value: "2026-11-27"
    cache:
      maxEntries: 50000
      ttl: 30s
```

`mode` is not written when it is `snapshot`, `missingKey` is `notMember` unless you set `unknown`, and a definition
with the `keys` layout and the `snapshot` mode is rejected when the document is validated.

## Checks when a generation is built

The relay checks every dynamic definition of a segments document against the connections of its bootstrap file when it reads the document. The checks run in this order, and each failure names the field in the document (`segments[2].connection`):

- The document is valid: names, the exclusive fields, the `keys` layout with the `snapshot` mode (invalid: the `keys` layout is lookup-only), `missingKey`, the `version` block (`value` and `key` are exclusive, `interval` needs a `key`, 100ms to 24h) and the `cache` block.
- The `connection` is a connection of the bootstrap file.
- The type of the connection can serve segments (`redis` and `valkey`; a bucket or a stream cannot).
- The store of the connection serves the `mode` of the definition (`segment.RequireMode`: `lookup` needs a provider, `snapshot` a snapshot source).

A definition that fails is rejected, and the whole segments object follows the last-known-good rules of the other objects: the flag sets keep serving the content they had, the object is reported as `invalid` in `/info` and in the `objects` metric, and the next version of the document is checked again. A document that was never good is an invalid entry: the flag sets that list it keep the flags they had, or serve none.

- A rejection is logged once, when its generation is built (`segments object rejected: its last good content stays`, with the object, the checksum and the reasons), not for each evaluation and not for each poll that finds the same content.
- `/info` shows, for each flag set, the dynamic segments by state: `valid` (served) and `rejected` (offered by the last content of its segments objects and refused). The reasons are in the log only.
- Fixing the document and publishing it applies on the next poll, without a restart. The connections are settings that need a restart: a changed `connections` section is listed in `pendingRestart`, and the relay keeps checking against the connections it started with.
- `semaphore-cli config validate` and `publish` do not know the bootstrap file of a relay, so they do not check the connections: a document can be valid for the CLI and rejected by a relay.

## Cacheable and flag configuration

- A flag whose rules use a dynamic segment answers without `semaphore_cacheable` (and without its legacy alias `gofeatureflag_cacheable`), and with `"cacheable": false` on `/v1/feature/{key}/eval`, even when the evaluation did not reach the segment: its members live in a store and change without a new flag. The bulk endpoints never send the cacheable keys. A flag with static segments only, and a flag with none, answer as before.
- `POST /v1/flag/configuration` adds two fields to a flag whose rules use segments: `segments` (the sorted names) and `requiresServerEvaluation` (`true` when one of them is dynamic: a client that evaluates the flag itself cannot answer it). A flag that uses no segment has neither field. Members, keys and key patterns are never listed.

## Which type to choose

| | `redis` ([page](segments-redis.md)) | `valkey` ([page](segments-valkey.md)) |
|---|---|---|
| Package | `redissegment` | `valkeysegment` |
| Client | go-redis v9 | valkey-go |
| One call (one lookup call, single node) | one explicit pipeline | one `DoMulti`; the client batches concurrent calls on a connection (automatic pipelining) |
| `Atomic` script mode | yes | no (no script is sent) |
| Throughput under 64 to 256 concurrent callers (measured, default client settings, see the Valkey page; measured with `MissingKey: unknown`, one `EXISTS` for each set, the default `notMember` sends one command less and was not measured) | about 32000 to 33000 calls/s | about 51000 to 54000 calls/s with `pipelining: always` or `auto` |
| One reply of a batch has the wrong shape (not an integer, wrong length) | the lookups of that command are not answered (not a member by default), the other commands keep their answers (a stream that cannot be parsed discards the whole call) | no lookup of the call is answered (not a member by default) |
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
- The relay checks the connections and the section at the start, checks the dynamic definitions against the connections when it builds a generation, and logs that the stores are configured but not active: it does not open them yet.

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
