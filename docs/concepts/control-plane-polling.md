# Control-plane polling

> Status: the manifest reader is in the code base (`internal/manifestreader`) but not active yet. It becomes active
> when the relay switches to config v2 (epic #19, issue #8). The v1 configuration keeps polling its retrievers as
> before.

## What it does

The relay learns what to serve from one small document, the **manifest**, that an external control plane publishes
to S3. The manifest lists the source objects (flag objects and flag-set documents) with a sha256 checksum each. The
manifest reader:

- polls the manifest with one conditional `GetObject` per interval (`If-None-Match` with the ETag it holds);
- fetches only the objects whose checksum it does not already hold;
- verifies every object against its listed checksum before serving it;
- hands the result to the rest of the relay as an immutable **snapshot**.

It never lists a bucket and never sends `HeadObject`.

## Request budget

Per replica and per control-plane connection:

| Situation | Requests |
|---|---|
| Nothing changed (the normal case) | exactly 1 conditional GET of the manifest per poll interval, answered with `304 Not Modified` |
| A new manifest that changes `K` objects | the manifest GET (200) plus `K` object GETs, once |
| Two entries with the same checksum | fetched once |
| An object that failed | 1 GET of that object per round until it succeeds, nothing else |
| Any situation | 0 LIST, 0 HEAD |

"One request" means one logical conditional GET. When a GET fails (5xx, throttling, a dropped connection) the S3
client retries it: those retries are extra attempts, and each attempt is counted once by the request metric. The
steady-state budget (every round answered `304`, no failing object) is exactly one request per round.

The numbers do not depend on how many objects, flag sets or API keys the manifest lists. With 60 s polling a replica
makes 1 440 requests a day; 200 replicas make 288 000. The manifest may list up to `maxObjects` entries (default
20000): a bigger manifest is rejected whole, one at the limit is accepted.

The poll interval is at least 1 s (default 60 s). It is spread by a jitter (default +/-10 %, always in [0, 1)), and
every wait is kept between 1 s and `interval x (1 + jitter)`. The first round is delayed by a random start phase,
`jitter x min(interval, 5 s)`, so replicas started together do not poll together. There is no start phase when the
jitter is disabled or when restored state served a snapshot at once.

## A poll round

1. Send the conditional GET of the manifest with the ETag of the last manifest read.
2. `304`: nothing else happens (no download, no parsing, no snapshot). The only exception is an object that failed
   earlier: it is fetched again (see below). Nothing that grows with the size of the manifest runs in such a round.
3. `200`: decode and validate the manifest with the `controlplane` package. An invalid manifest is rejected whole.
4. Apply the generation rules.
5. Fetch the objects whose checksum is not held, at most 4 at a time (configurable, at most 16), verify each
   with its sha256, and publish one snapshot for the generation.

A failed object never blocks the others.

## Generation rules

| Manifest | Result |
|---|---|
| Higher generation than the held one | accepted |
| Lower generation | ignored and reported (`lower-generation`) |
| Equal generation, same objects, checksums, kinds and formats | accepted, nothing to do (a rewrite of the same content; a changed `key`, `connection` or `createdAt` alone changes nothing) |
| Equal generation, another object set, checksum, `kind` or `format` | rejected and reported (`generation-conflict`): a publisher bug |

A rollback is a new, higher generation that references older objects.

## Object verification and states

Each object of a snapshot has a state:

| State | Meaning |
|---|---|
| `ok` | The bytes are the verified content of the checksum the manifest of this generation lists |
| `stale` | Fetching or verifying the listed content failed, but an earlier verified content of the same name exists: the snapshot carries those bytes with their own checksum |
| `missing` | Fetching or verifying failed and no content of this name was ever loaded: no bytes |

Every failed object has a reason: `missing` (404), `checksum-mismatch`, `size-mismatch`, `invalid-size`, `too-large`,
`denied`, `timeout`, `error`, `unknown-connection`.

The listed `size` is binding:

- an entry with a size below 1 fails with `invalid-size`, and one above the object limit with `too-large`, without
  any request;
- the request is limited to the listed size, and a body shorter or longer than it fails with `size-mismatch`, before
  the checksum is looked at;
- an entry that names a connection the reader has no store for fails with `unknown-connection`, without any request;
  it is never read from the store of the manifest. An entry with a `versionId` is read at that version.

A failure that needs no request is final until the manifest changes: it costs nothing in later rounds.

A failed object is tried again at the next round, and only that object: the verified ones are not fetched again. When it
succeeds, a new snapshot of the **same generation** is published with the new state. A consumer must accept a state
change inside a generation.

Verified bytes are cached by checksum, so two names with one checksum share one download and one copy. A cached
copy is dropped when no entry of the current manifest and no stale name refers to it.

When several entries list one checksum, the content is fetched through the first one. Only if that fails is the next
entry tried (one more request per extra entry, only on failure). All names that share the checksum get the same
result.

## What happens on each failure

| Failure | What the reader does |
|---|---|
| Manifest unreachable, 5xx, 403, throttled, timeout | Keeps the previous snapshot, reports the poll result and reason in the status, tries again at the next interval. Failed objects are not retried in that round: during an outage the reader adds no object request to the failing manifest request, to keep the request budget |
| Manifest deleted (404) | Same: the previous snapshot stays, result `not_found` |
| Manifest larger than the byte limit | Not read, previous snapshot stays (`too_large`) |
| Manifest over `maxTotalBytes` | Rejected whole (`too-large-total`) before any object request, previous snapshot stays |
| Manifest does not decode or validate | Rejected whole, previous snapshot stays. Its ETag is kept, so the same bytes are not downloaded and reported again; a corrected manifest has other bytes and is read |
| Manifest of another channel, unsupported `apiVersion`, wrong `kind` | Rejected whole, previous snapshot stays |
| Lower generation, generation conflict | Rejected, reported, previous snapshot stays |
| Object missing, corrupt, of the wrong size or timed out | That object is `stale` (or `missing`), the others apply |
| Object download error | Same, with a retryable reason |
| A hook panics | The panic is recovered, logged once per hook, counted in the status; polling goes on |

The reader never stops the process. Error back-off and the on-disk state copy come in a later change; until then a
failed round is simply tried again at the next interval.

## Limits

| Limit | Default | Bound |
|---|---|---|
| Objects in a manifest | 20000 | The manifest is rejected whole above it |
| Manifest size | 16 MiB | The download is refused without buffering above it |
| Object size | 16 MiB, and the `size` the manifest lists | An entry above it is not requested; a body of another length fails |
| Sum of the sizes of the distinct objects | 1 GiB | The manifest is rejected whole above it |
| Request timeout | 60 s | Every request (manifest and each object) |
| Concurrent object downloads | 4 | At most 16 |
| Poll interval | 60 s, +/-10 % jitter | At least 1 s |

Cost of a large manifest: decoding 20000 entries takes about 190 ms and allocates about 144 MB, on a commodity
machine. The decoder builds a tree of the whole document before the entry count is checked, so a hostile manifest near
the byte cap can allocate far more before it is rejected. The manifest byte cap is what bounds that cost. This cost
is paid only when the manifest changes, never on a `304`.

Worst-case memory: about the total-size cap for the current content, plus the same again for stale content that is
still carried, plus the concurrent downloads times the object limit in flight. Object bodies are read into one
buffer sized from the listed size.

## Delivery and status

- Snapshots go through a Go channel of size 1 with latest-wins delivery: a snapshot that was not consumed is replaced
  by a newer one, and the poller never waits for the consumer.
- The status is an immutable value readable at any time: last poll result and reason, held generation, last sync, the
  state counts and the failed objects (name, state, reason, no bytes), the counts of polls and `304` answers, and
  the hooks that panicked.
- Hooks (restore, save, round) run on the poller and must not block. Their arguments are read-only and shared with
  the reader: copy what you keep. The save hook always gets the manifest that was accepted, with its own ETag.
- After a restart from saved state, the saved ETag is used only if the saved manifest is accepted. Otherwise the
  first poll is an unconditional GET.
- Logs and the status carry object names and reason codes only: never object bytes, keys or error text of the store.

## Metric

`semaphore_s3_requests_total{connection,operation,result}` counts every attempt of every S3 request of a connection:

- `operation`: `get`, `list`, `head`, `put`, `delete` or `other`
- `result`: `ok`, `not_modified`, `not_found`, `throttled`, `client_error`, `redirect`, `server_error` or `error`

A request retried after a `503` counts twice. A `304` is `not_modified`, never an error. The only labels are the
connection name and the two closed sets: there is no per-flag or per-object series. With the manifest reader the
steady state is one `get`/`not_modified` per interval per connection.

See also [Connections](connections.md).
