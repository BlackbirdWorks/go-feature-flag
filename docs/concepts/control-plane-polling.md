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

The numbers do not depend on how many objects, flag sets or API keys the manifest lists. With 60 s polling a replica
makes 1 440 requests a day; 200 replicas make 288 000. The manifest may list up to `maxObjects` entries (default
20000): a bigger manifest is rejected whole, one at the limit is accepted.

The poll interval is spread by a jitter (default +/-10 %), so replicas do not poll at the same instant.

## A poll round

1. Send the conditional GET of the manifest with the ETag of the last manifest read.
2. `304`: nothing else happens (no download, no parsing, no snapshot). The only exception is an object that failed
   earlier: it is fetched again (see below).
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
| Equal generation, same objects and checksums | accepted, nothing to do (a rewrite of the same content) |
| Equal generation, another object set or checksum | rejected and reported (`generation-conflict`): a publisher bug |

A rollback is a new, higher generation that references older objects.

## Object verification and states

Each object of a snapshot has a state:

| State | Meaning |
|---|---|
| `ok` | The bytes are the verified content of the checksum the manifest of this generation lists |
| `stale` | Fetching or verifying the listed content failed, but an earlier verified content of the same name exists: the snapshot carries those bytes with their own checksum |
| `missing` | Fetching or verifying failed and no content of this name was ever loaded: no bytes |
| `pending` | Not fetched yet (reserved for a restart without state; the reader does not publish it today) |

Every failed object has a reason: `missing` (404), `checksum-mismatch`, `too-large`, `denied`, `error`,
`unknown-connection`.

A failed object is tried again at the next round, and only that object: the verified ones are not fetched again. When it
succeeds, a new snapshot of the **same generation** is published with the new state. A consumer must accept a state
change inside a generation.

Verified bytes are cached by checksum, so two names with one checksum share one download and one copy. A cached
copy is dropped when no entry of the current manifest and no stale name refers to it.

## What happens on each failure

| Failure | What the reader does |
|---|---|
| Manifest unreachable, 5xx, 403, throttled | Keeps the previous snapshot, reports the poll result and reason in the status, tries again at the next interval |
| Manifest deleted (404) | Same: the previous snapshot stays, result `not_found` |
| Manifest larger than the byte limit | Not read, previous snapshot stays (`too_large`) |
| Manifest does not decode or validate | Rejected whole, previous snapshot stays. Its ETag is kept, so the same bytes are not downloaded and reported again; a corrected manifest has other bytes and is read |
| Manifest of another channel, unsupported `apiVersion`, wrong `kind` | Rejected whole, previous snapshot stays |
| Lower generation, generation conflict | Rejected, reported, previous snapshot stays |
| Object missing, corrupt or too large | That object is `stale` (or `missing`), the others apply |
| Object download error | Same, with a retryable reason |

The reader never stops the process. Error back-off and the on-disk state copy come in a later change; until then a
failed round is simply tried again at the next interval.

## Limits

| Limit | Default | Bound |
|---|---|---|
| Objects in a manifest | 20000 | The manifest is rejected whole above it |
| Manifest size | 32 MiB | The download is refused without buffering above it |
| Object size | 16 MiB, and the `size` the manifest lists | A larger download is refused without buffering above it |
| Concurrent object downloads | 4 | At most 16 |
| Poll interval | 60 s, +/-10 % jitter | |

## Delivery and status

- Snapshots go through a Go channel of size 1 with latest-wins delivery: a snapshot that was not consumed is replaced
  by a newer one, and the poller never waits for the consumer.
- The status is an immutable value readable at any time: last poll result and reason, held generation, last sync, the
  state counts and the failed objects (name, state, reason, no bytes), the counts of polls and `304` answers.
- Logs and the status carry object names and reason codes only: never object bytes, keys or error text of the store.

## Metric

`semaphore_s3_requests_total{connection,operation,result}` counts every attempt of every S3 request of a connection:

- `operation`: `get`, `list`, `head`, `put`, `delete` or `other`
- `result`: `ok`, `not_modified`, `not_found`, `throttled`, `client_error`, `redirect`, `server_error` or `error`

A request retried after a `503` counts twice. A `304` is `not_modified`, never an error. The only labels are the
connection name and the two closed sets: there is no per-flag or per-object series. With the manifest reader the
steady state is one `get`/`not_modified` per interval per connection.

See also [Connections](connections.md).
