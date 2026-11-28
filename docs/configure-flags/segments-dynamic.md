# Segments - dynamic

A dynamic segment is a segment of type `redis` or `valkey`: the members stay in the store and are looked up while a flag is evaluated. See [Segments](segments.md) for what a segment is, the rule syntax and the two answers (member, not a member) and the opt-in for unknown. For small lists that change rarely use a [static segment](segments-static.md).

- The relay fetches every membership that an evaluation needs in one batch before the rules run: one flag, or all the flags of a bulk request, give one call to the store for each connection (and each `missingKey` policy) the request reaches. One round trip to a single node, one batch for each node on a cluster. A membership that the batch could not read (an attribute inside a typed object) is asked when the rule reaches it.
- The providers work alone, but then every lookup goes to the store. When the resolver is used, it adds a bounded cache with a short negative TTL, coalescing of identical lookups and a circuit breaker for each segment. Errors are never cached.
- When the store cannot answer a lookup, the lookup is not a member by default, and the failure policy of the segment (`onError`) can change that (`unknown` is the opt-in of the three-valued logic). The evaluation status is `STORE_ERROR` for the lookups that were not answered. A snapshot that was loaded and then failed to refresh is not an unanswered lookup: it keeps answering from the last known good set and the status is `STALE`. With the store down, a request takes at most its deadline plus a small margin. Exception: for a `valkey` segment whose peer stops reading until the connection ring is full, the wait can last until the `ConnWriteTimeout` of the client closes the connection (about 10 seconds by default), see [Limits](segments-valkey.md#limits).
- The provider only reads, so a read-only ACL user is enough.

## Modes

> The relay serves the `lookup` mode. A definition in the `snapshot` mode (the default when `mode` is not written) is rejected alone while the relay serves dynamic segments, with the reason `the snapshot mode is not served yet by this relay: use mode: lookup`: it answers "not a member" and the evaluation reports `NOT_CONFIGURED`. The snapshot mode comes with the next step of [#16](https://github.com/BlackbirdWorks/semaphore/issues/16). Write `mode: lookup` in every definition until then. The modes are interfaces and reference implementations in `modules/core/segment`.

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

The relay checks a segments document when it reads it. The connections are settings of the process (the bootstrap file), the definitions are published content that several relays share: a relay that lacks a connection must not stop serving its flag sets. So there are two levels.

The document is checked as a whole. It is valid: names, the exclusive fields, the `keys` layout with the `snapshot` mode (invalid: the `keys` layout is lookup-only), `missingKey`, the `version` block (`value` and `key` are exclusive, `interval` needs a `key`, 100ms to 24h) and the `cache` block. A document that is not valid is rejected whole and follows the last-known-good rules of the other objects: the flag sets keep serving the content they had, the object is `invalid` in `/info` and in the `objects` metric, and a document that was never good is an invalid entry (the flag sets that list it keep the flags they had, or serve none).

Each dynamic definition is then checked against the connections of the bootstrap file, one by one. In this order, and each failure names the field in the document (`segments[2].connection`):

- The `connection` is a connection of the bootstrap file.
- The type of the connection can serve segments (`redis` and `valkey`; a bucket or a stream cannot).
- The store of the connection serves the `mode` of the definition (`segment.RequireMode`: `lookup` needs a provider, `snapshot` a snapshot source).

A definition that fails is rejected alone. The document and its other definitions are applied (the static segments work, the other dynamic ones too), and the flag set is served, on the first load and later. The rejected segment stays in the catalog as "not configured": it answers like a segment without a provider (not a member, status `NOT_CONFIGURED`), and the flags that use it are not cacheable. A rejected definition never grants membership, whatever its `onError` says (even `member`) and whatever the rule says: a rejected segment answers a known "not a member", so a negated clause (`not (... in segment(...))`) matches, and `onError: match` does not apply. A missing or unfit connection is a configuration error of this relay, not a store outage that the publisher chose a policy for.

- A rejection is logged once, when its generation is built (`dynamic segment definitions rejected: ...`, with the object, the checksum and the path-addressed reasons), not for each evaluation and not for each poll that finds the same content.
- `/info` shows, for each flag set, the dynamic segments by state: `valid` and `rejected` (in `segments.dynamicStates`). The same counts are the series `semaphore_resource_count{kind="dynamicSegment", state="valid"|"rejected"}`. The reasons are in the log only.
- Fixing the document and publishing it applies on the next poll, without a restart. Fixing a connection is a change of the bootstrap file, which needs a restart (a changed `connections` section is listed in `pendingRestart`): the relay keeps checking against the connections it started with.
- `semaphore-cli config validate` and `publish` do not know the bootstrap file of a relay, so they do not check the connections: a document can be valid for the CLI and have definitions that a relay rejects.

## Cacheable and flag configuration

- A flag whose rules use a dynamic segment answers without `semaphore_cacheable` (and without its legacy alias `gofeatureflag_cacheable`), and with `"cacheable": false` on `/v1/feature/{key}/eval`, even when the evaluation did not reach the segment, and even when the flag wrote these keys in its own `metadata`: they are removed from the single and the bulk answers. Its members live in a store and change without a new flag. The bulk endpoints never send the cacheable keys of an evaluation. A flag with static segments only, and a flag with none, answer as before.
- `POST /v1/flag/configuration` adds two fields to a flag whose rules use segments: `segments` (the sorted names, also when the flag set cannot resolve a name) and `requiresServerEvaluation` (`true` when one of them is dynamic or cannot be resolved by the flag set: a client that evaluates the flag itself cannot answer it). A flag that uses no segment has neither field. Members, keys and key patterns are never listed.

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
- The segment definitions are published with the flags, like any other flag content; the bootstrap file has no `segments.definitions`.

## Lifecycle

- The relay opens no store at the start. A connection is opened when a valid dynamic definition first uses it, by a loop that the run context of the relay owns (it dials again every second while the store is not reachable). One client serves every generation: a reload binds the definitions of the new generation to it, and never closes it.
- Each generation has its own view of the segments: the definitions of its flag sets bound to the shared clients, each behind a resolver (cache, coalescing, circuit breaker). The view is built off to the side and swapped in with the runtime, in the same atomic swap. A flag set that a reload does not change keeps its view (and its cache); a flag set that changes gets a new one. A request keeps the view it loaded, so an evaluation is never cut by a swap.
- A store that cannot be reached at the start never stops the relay and never makes it unready. Its segments answer "not a member" with `STORE_ERROR` until it is back. The relay stops the clients at shutdown, with the shutdown timeout, after the loops ended.
- A version `value` in the definition is the version of the cache of the generation. A version `key` is read by a watcher (a loop owned by the run context) every `version.interval` (5s when it is not written), never on the request path: a changed version makes the cached answers misses, and a request never waits for it.

## What happens when the store is down

| Situation | Answer | Status | Relay |
|---|---|---|---|
| Healthy store | the members of the store | none | ready |
| Never connected, or gone, and the answer is not cached | not a member (or what `onError` says) | `STORE_ERROR` | ready |
| Gone, and the answer is cached (a member for the TTL, a non-member for the negative TTL) | the cached answer | none | ready |
| Breaker open (consecutive failures) | not a member, the store is not called | `STORE_ERROR` | ready |
| Store is back | the members of the store, after the breaker cooldown | none | ready |
| Definition rejected (unknown connection, a kind that cannot serve the mode, the `snapshot` mode) | not a member | `NOT_CONFIGURED` | ready |

`/info` lists each connection (`segmentStores`: `idle`, `connecting`, `connected`, `error` or `closed`) and, for each flag set, the served dynamic segments by health (`dynamicStates.healthy` and `dynamicStates.storeError`). The metrics are in [probes](../relay-proxy/probes.md).

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
