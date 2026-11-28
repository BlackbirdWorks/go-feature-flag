# Inline lists or dynamic segments

A segment is a list of ids that a rule tests with `targetingKey in segment("name")`. There are three ways to hold the list. This page helps you choose, says what a reader can observe after a change, gives sizes from measurements, and shows how to move a large inline list into a store. See [Segments](segments.md) for the rule syntax.

## Choose

| | Static (inline `members`) | Dynamic `snapshot` | Dynamic `lookup` |
|---|---|---|---|
| Where the members live | in the published document | in a Valkey or Redis set; the relay holds a copy in memory | in the store; the relay keeps a bounded cache of answers |
| Size that fits | up to a few thousand ids | up to the memory guard (10,000,000 members); about 70 bytes of relay heap for each member | any size; the memory is the cache only |
| Cost of an evaluation | a hash lookup | a hash lookup, no store call | a cache hit costs the same; a miss is one batched call to the store |
| Change of a member | publish a new document | write to the store and bump the version key | write to the store (bump the version key to cut the cache short) |
| When the store is down | not applicable | the last good set keeps answering, `STALE` | cached answers keep answering, the rest is `STORE_ERROR` |
| Needs a store | no | yes | yes |

Rules of thumb:

- A list that changes only with a flag change and has fewer than a few thousand ids: keep it [static](segments-static.md). It has no failure mode.
- A list that is large or changes without a flag change, and every evaluation must be fast and independent of the store: `snapshot`. It is the default of a store-backed segment. You pay memory and a delay of one version interval.
- A list too large for memory, or where only a few ids are ever asked: `lookup`. You pay one call to the store for each request that the cache does not answer.
- Use both in one relay: the modes are chosen for each segment.

## What a reader sees after a change

| Mode | A member is added or removed | Bounded by |
|---|---|---|
| Static | on the next poll of the control plane after the publish | the manifest poll interval |
| `snapshot` | after the loop reads a new version key value and pulls the set, then swaps it in at once | `version.interval` (5s when not written), plus the time to pull the set |
| `snapshot`, no version key | after the next refresh pulls the whole set | the refresh interval; costs a full read for each poll |
| `lookup`, with `version.key` | a cached answer is a miss as soon as the watcher reads the new version; an id that is not cached is read at once | `version.interval` |
| `lookup`, no version key | an id that is cached keeps its answer until it expires; an id that is not cached is read at once | `ttl` (30s) for a member, `negativeTtl` (5s) for a non-member |

- A writer that changes a set without bumping the version key is seen only after the cache expires (`lookup`), or never (`snapshot` with a version key). Always bump the key last, after the members.
- Replace a set atomically (build it under another key and `RENAME` it over the old one), and bump the version after it; the version must never return to an earlier value (a counter or a timestamp, not a content hash). A deleted set means nobody is in the segment.
- A `snapshot` set that is replaced while a relay pulls it can be served as a mix of old and new members until the next poll, unless `version.debounce` is set. It is off by default; turn it on for sets that are replaced by a swap or an import while relays poll, with a value near the poll interval. Under steady edits the relay is at most `version.interval` + `debounce` + one pull behind the store. See [Debounce](segments-dynamic.md#debounce).
- Two relays read the store separately, so they can differ by up to one interval for a moment.

## Sizing

Measured with a real Valkey and a real relay on a commodity 8-thread Linux machine, loopback, 8 sets of 1,000,000 members, 300 requests per second (open loop), flags that name 1, 3 and 8 segments (`task test-segment-load`; see [Measured](segments-dynamic.md#measured)):

- `snapshot`: about 72 to 75 bytes of relay heap for each member (1,000,000 members: about 75 MB; 8,000,000 members: about 570 MB). The store holds a member in about 43 bytes. Plan the memory of the relay for the sum of all sets it holds, plus headroom for the new set that is built beside the old one during a refresh.
- `snapshot` first load: about 2 seconds for a 1,000,000-member set, about 6 seconds for eight of them. The relay is ready before it; the segment answers `STORE_ERROR` until the set is loaded.
- Commands to the store: `snapshot` sends none on the request path (the loop reads one version key per set each interval, and the set when it changed). `lookup` with a warm cache sends none; with a cold cache it sends one `SMISMEMBER` for each segment that a flag names, in one round trip for the request (1, 3 and 8 commands for flags of 1, 3 and 8 segments).
- Latency: at this rate the p99 of a whole request, HTTP included, stayed under 2 ms in both modes and with a cold or a warm cache. A cold lookup costs a little under 1 ms more at the median. The numbers are recorded by the test, not asserted: measure on your hardware.
- Set sizes tested: 1,000,000 members for each of 8 sets, so 8,000,000 members for one relay.

Size the cache of the `lookup` mode (`maxEntries`, default 10,000) for the ids you ask often, not for the set.

## Key layouts

- `set` (default): one Redis or Valkey set; a lookup is `SMISMEMBER`, a snapshot reads it with `SSCAN`. Use a hash tag for the key (`semaphore:segment:{churned}`) when the store is a cluster, so the set and its version key hash to one slot when you write both in one transaction.
- `keys` (`lookup` only): one key for each member, `<prefix><name>:<id>`; a lookup is `EXISTS` for each id. Use it when members expire on their own (a key with a time to live). It cannot be listed, so it is never a `snapshot`.
- Version key: any key whose value changes when the set changes (a counter or a timestamp). Both modes read it with `GET` off the request path.

See [the Valkey page](segments-valkey.md) and [the Redis page](segments-redis.md) for the options.

## Failure policy

When the store cannot answer, a lookup is not a member by default, the status is `STORE_ERROR`, and the relay stays ready. A `snapshot` that was loaded keeps answering from the last good set with `STALE`. The segment `onError` setting can change the answer. The full list, row by row, is the table [What happens when the store is down](segments-dynamic.md#what-happens-when-the-store-is-down); the chaos tests check each row (kill, pause, added latency), and the [runbook](../relay-proxy/runbook.md#segment-store-outages) says what to look at.

## Move a large inline list into a store

The ids below are in a static segment today:

<!-- segments-doc -->
```yaml
apiVersion: semaphore/v1
kind: Segments
segments:
  - name: churned
    members: [u-1, u-2, u-3]
```

1. Write the ids to a file, one id per line (`ids.txt`).
2. Load them into the store in batches (the key and the host are examples; use a read-write user):

   ```
   xargs -n 1000 < ids.txt | sed 's/^/SADD semaphore:segment:{churned} /' | valkey-cli -h valkey.internal
   ```

3. Write the version key last:

   ```
   valkey-cli -h valkey.internal SET semaphore:segment:churned:version "$(date +%s)"
   ```

4. Check the count: `valkey-cli -h valkey.internal SCARD semaphore:segment:{churned}` equals the number of lines of `ids.txt`.
5. Publish the same segment as a dynamic one. The name stays, so no rule changes:

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

6. Wait for the set to load: `semaphore_segment_snapshot_sets{state="loaded"}` counts it, and `semaphore_segment_snapshot_members` shows the members held. Until then the segment answers `STORE_ERROR`; publish when the relays are ready for it, or add the connection first and the segment later.
7. From now on change the set with `SADD` and `SREM`, then bump the version key.

To go back, publish the inline document again.
