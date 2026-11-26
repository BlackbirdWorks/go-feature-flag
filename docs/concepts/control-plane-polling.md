# Control-plane polling

> Status: the manifest reader and its state directory are in the code base (`internal/manifestreader`) but not active yet. It becomes active
> when the relay switches to config v2 (epic #19, issue #8). The v1 configuration keeps polling its retrievers as
> before.

## Keys are absolute bucket keys

The manifest key (`controlPlane.manifest.key`) and the `key` of every object entry are absolute bucket keys, exactly as
the publisher wrote them. A key prefix is applied by the publisher only: the reader never adds one, and fetches
exactly the key it is given. A key that is empty or starts with `/` is not requested: an object entry fails with
`invalid-key` (the manifest key is refused when the reader is built, and an entry with an empty key makes the whole
manifest invalid).

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
| Nothing changed and no object failing (the normal case) | exactly 1 conditional GET of the manifest per poll interval, answered with `304 Not Modified`, and nothing else |
| A new manifest that changes `K` objects | the manifest GET (200) plus `K` object GETs, once |
| Two entries with the same checksum | fetched once |
| Objects that failed | the manifest GET plus 1 GET for each failing object that is due, on a growing, capped schedule (see [Backoff](#backoff)); for an object that failed for good, one every 5 minutes (plus 1 more for each other entry that lists the same checksum, only when the first fails) |
| Any situation | 0 LIST, 0 HEAD |

"One request" means one logical conditional GET. When a GET fails (5xx, throttling, a dropped connection) the S3
client retries it: those retries are extra attempts, and each attempt is counted once by the request metric. The
steady-state budget (every round answered `304`, no failing object) is exactly one request per round. With failing
objects a round costs the manifest GET plus the object retries, and nothing else.

The request numbers do not depend on how many objects, flag sets or API keys the manifest lists. With 60 s polling a replica
makes 1 440 requests a day; 200 replicas make 288 000. The manifest may list up to `maxObjects` entries (default
20000): a bigger manifest is rejected whole, one at the limit is accepted.

The poll interval is at least 1 s (default 60 s). It is spread by a jitter (default +/-10 %, always in [0, 1)), and
every wait is kept between 1 s and `interval x (1 + jitter)`. The first round is delayed by a random start phase,
`jitter x min(interval, 5 s)`, so replicas started together do not poll together. There is no start phase when the
jitter is disabled or when restored state served a snapshot at once.

## A poll round

1. Send the conditional GET of the manifest with the ETag of the last manifest read.
2. `304`: when no object is failing, nothing else happens (no download, no parsing, no snapshot). Otherwise each
   failing object is fetched again (see below). Nothing that grows with the size of the manifest runs in such a round
   as long as no object changes state. A round in which an object recovers or fails in another way rebuilds the
   snapshot and the status, which costs time and memory proportional to the size of the manifest.
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
| Equal generation, same channel, objects, checksums, kinds and formats | accepted. A rewrite that changes only `createdAt`, or the `key` or `connection` of an entry that already serves its checksum (the connection being a known one), changes nothing and publishes no snapshot. For an entry that is failing, a new `key` or `connection` is a new route: it is tried in that round, and a recovery snapshot of the same generation follows if it works. The entry checks still apply to the new entry: an unknown `connection`, an invalid `key` or another `size` than the content fails that entry (`unknown-connection`, `invalid-key`, `size-mismatch`), which is then `stale` (or `missing`) and published as a state change |
| Equal generation, another channel, object set, checksum, `kind` or `format` | rejected and reported (`generation-conflict`): a publisher bug |

A rollback is a new, higher generation that references older objects.

## Object verification and states

Each object of a snapshot has a state:

| State | Meaning |
|---|---|
| `ok` | The bytes are the verified content of the checksum the manifest of this generation lists |
| `stale` | Fetching or verifying the listed content failed, but an earlier verified content of the same name exists: the snapshot carries those bytes with their own checksum |
| `missing` | Fetching or verifying failed and no content of this name was ever loaded: no bytes |

Every failed object has a reason: `missing` (404), `checksum-mismatch`, `size-mismatch`, `invalid-size`, `too-large`,
`denied`, `timeout`, `error`, `unknown-connection`, `invalid-key`, `not-restored` (restored from the state directory, fetched after the first live poll).

The listed `size` is binding:

- an entry with a size below 1 fails with `invalid-size`, and one above the object limit with `too-large`, without
  any request;
- the request is limited to the listed size, and a body shorter or longer than it fails with `size-mismatch`, before
  the checksum is looked at;
- an entry whose key starts with `/` fails with `invalid-key`, without any request;
- an entry that names a connection the reader has no store for fails with `unknown-connection`, without any request;
  it is never read from the store of the manifest. An entry with a `versionId` is read at that version.

A cached object is also checked against the size of each entry that uses it: an entry that lists another size than
the content has fails with `size-mismatch`, and the others that share the checksum are not affected.

The checks of one entry (`invalid-key`, `invalid-size`, `too-large`, `unknown-connection`) apply to that entry only,
even when another entry lists the same checksum, or the content is already held: an entry that fails them is failed
with its own reason and never takes the result of a sibling. Such a failure needs no request and is final until the
manifest changes. When every route to a checksum fails, the failure is retryable if any route failed retryably, and
its reason is the most actionable one: `denied`, then `checksum-mismatch`, `size-mismatch`, `missing`, then the rest.

A failed object is tried again on its own schedule (see [Backoff](#backoff)), and only that object: the verified ones
are not fetched again. When it succeeds, a new snapshot of the **same generation** is published with the new state. A consumer must accept a state
change inside a generation.

Verified bytes are cached by checksum, so two names with one checksum share one download and one copy. A cached
copy is dropped when no name serves it (an `ok` name, or a `stale` one that carries it) and no retry for it is
pending. An entry that failed one of its own checks pins nothing.

When several entries list one checksum, the content is fetched through the first one. Only if that fails is the next
entry tried (one more request per extra entry, only on failure). All names that share the checksum get the same
fetched content; each entry still passes its own checks (below).

## What happens on each failure

| Failure | What the reader does |
|---|---|
| Manifest unreachable, 5xx, 403, throttled, timeout | Keeps the previous snapshot, reports the poll result and reason in the status, and backs off (see [Backoff](#backoff)). Failed objects are not retried in that round: during an outage the reader adds no object request to the failing manifest request, to keep the request budget |
| Manifest deleted (404) | Same: the previous snapshot stays, result `not_found`, and the poll backs off |
| Manifest larger than the byte limit | Not read, previous snapshot stays (`too_large`) |
| Manifest over `maxTotalBytes` | Rejected whole (`too-large-total`) before any object request, previous snapshot stays |
| Manifest does not decode or validate | Rejected whole, previous snapshot stays. Its ETag is kept, so the same bytes are not downloaded and reported again; a corrected manifest has other bytes and is read |
| Manifest of another channel, unsupported `apiVersion`, wrong `kind` | Rejected whole, previous snapshot stays |
| Lower generation, generation conflict | Rejected, reported, previous snapshot stays |
| Object missing, corrupt, of the wrong size or timed out | That object is `stale` (or `missing`), the others apply |
| Object download error | Same, with a retryable reason |
| A hook panics | The panic is recovered, logged once per hook, counted in the status; polling goes on |

The reader never stops the process.

## Limits

| Limit | Default | Bound |
|---|---|---|
| Objects in a manifest | 20000 | The manifest is rejected whole above it |
| Manifest size | 16 MiB | The download is refused without buffering above it |
| Object size | 16 MiB, and the `size` the manifest lists | An entry above it is not requested; a body of another length fails |
| Sum of the sizes of the distinct objects | 1 GiB | The manifest is rejected whole above it. An entry above the object limit counts for nothing (it is not requested); each size is compared with the budget left, so the sum cannot overflow |
| Request timeout | 60 s | Every request (manifest and each object) |
| Concurrent object downloads | 4 | At most 16 |
| Poll interval | 60 s, +/-10 % jitter | At least 1 s |
| Backoff cap (`BackoffMax`) | 5 min | Never below the poll interval |
| Fetch spreading window (`FetchSpread`) | 5 s | Never above the poll interval |

Cost of a large manifest: decoding 20000 entries takes about 190 ms and allocates about 144 MB, on a commodity
machine. The decoder builds a tree of the whole document before the entry count is checked, so a hostile manifest near
the byte cap can allocate far more before it is rejected. The manifest byte cap is what bounds that cost. This cost
is paid only when the manifest changes, never on a `304`.

Worst-case memory: about the total-size cap for the current content, plus the last good content that stale names still
carry, plus the concurrent downloads times the object limit in flight. A carried object was within the limits when it
was loaded, but the carried bytes of several older generations are not bounded by the total-size cap: they are bounded
by the number of objects times the object limit. A checksum listed with several sizes counts with the largest size
against the total-size cap. Object bodies are read into one
buffer sized from the listed size.

## Delivery and status

- Snapshots go through a Go channel of size 1 with latest-wins delivery: a snapshot that was not consumed is replaced
  by a newer one, and the poller never waits for the consumer.
- The status is an immutable value readable at any time: last poll result and reason, held generation, last sync, the
  state counts and the failed objects (name, state, reason, no bytes), the counts of polls and `304` answers, the verified objects held in memory (count and bytes), and
  the hooks that panicked.
- Hooks (restore, save, round) run on the poller and must not block. Their arguments are read-only and shared with
  the reader: copy what you keep. The save hook always gets the manifest that was accepted, with its own ETag.
- After a restart from saved state, the saved objects are served at once, nothing is fetched before the first poll of
the live manifest, and an object that was not saved is `missing` with reason `not-restored` until it is fetched.
Restoring is not a poll round: the round hook runs for live polls only. A saved manifest that is not accepted is
dropped with its objects.
The saved ETag is used only if the saved manifest is accepted. Otherwise the
  first poll is an unconditional GET.
- Logs and the status carry object names and reason codes only: never object bytes, keys or error text of the store.

## Backoff

All the timing goes through the scheduler of the reader, so a test (or an operator reading the code) sees the exact
waits it asks for.

**Manifest poll.** After `n` consecutive failed polls (an error, a timeout, a `404`, a manifest above the byte limit)
the wait before the next round is a full jitter draw:

```text
ceiling(n) = min(BackoffMax, pollInterval x 2^n)           n + 1 when the failure was a throttle
wait       = max(regular wait, random(0, 1) x ceiling)
```

- With the defaults the ceilings are 120 s, 240 s, 300 s, 300 s, ... (a throttle: 240 s, 300 s, ...).
- A failure never makes the reader poll faster than the regular wait.
- Throttling (`429`, `SlowDown`, other throttle codes) backs off one step further than another error.
- The first success resets everything: a `200` or a `304`. A manifest that was read and rejected (invalid, lower
  generation, another channel) is not a failure of the poll.
- A poll that is retried inside the SDK is still one poll here; the extra attempts show in the request metric.

**Failed objects.** A failed object has its own schedule, so one broken object does not cost one GET per round forever:

- A retryable failure (`missing`, `checksum-mismatch`, `size-mismatch`, `timeout`, `error`) waits
  `random(0, 1) x min(BackoffMax, pollInterval x 2^(n-1))` after its `n`th failure in a row: at most one poll
  interval for the first retry, then 2x, 4x, up to 5 min.
- A failure that is not retryable (`denied`) is tried again every `BackoffMax` (5 min), a slow fixed cadence: a retry
  cannot help until the publisher or the configuration changes.
- A new manifest generation tries every failed object at once (after the spreading delay).
- Only a round whose manifest GET worked retries objects. During an outage no object request is added.
- The object schedule runs on the sum of the waits the scheduler completed, not on a clock.

Example, a permanently missing object, jitter draws of 0.5, 60 s polls: tried in round 0, 1, 2, 4, 7, 10 (6 GETs in
11 rounds instead of 11). A denied object: rounds 0, 5, 10 (3 GETs).

## Fetch spreading

When a new generation replaces content that the reader already holds, the reader waits `random(0, 1) x
min(FetchSpread, pollInterval)` (default up to 5 s) before it fetches the objects, so 200 replicas that read the same
manifest do not send their object requests in the same instant. It costs no request.

- No delay on a cold start with no content: nothing is served yet, so the first manifest is fetched at once.
- No delay for the retry of a failed object in the same generation.
- `DisableFetchSpread` turns it off.
- Phase shift: the delay is inside the round, and the wait to the next round starts after the round ends, so a
  replica that waited `d` polls `d` later from then on (up to 5 s). The shift is permanent and different for every
  replica, which is the point: replicas drift apart instead of polling together. It does not change the number of
  requests.
- The delay is counted in the virtual time of the object schedule like any other wait.

## State directory

`internal/manifestreader/statedir` keeps the last accepted manifest and its verified objects on disk. It plugs into
the reader through the `Hooks` (`Restore`, `Save`, `StateStatus`): the reader does not know about files.

**What a restart costs**

| Situation | Requests |
|---|---|
| Restart, nothing changed | 1 conditional GET (`If-None-Match` with the ETag of the accepted manifest), answered `304`; 0 object GETs; the snapshot is served before that request |
| Restart, store unreachable | The failed poll only; the saved content is served at once |
| An object file is missing or corrupt | One GET for that object, in the first round, after the restored snapshot was published (the object is `missing` with reason `not-restored` until then) |
| The manifest copy is missing or corrupt, empty directory | A cold start: the manifest and the objects |
| The directory is refused (see below) | A cold start; nothing is read from or written to the directory |

The restored snapshot is published before any request: verified objects are `ok`, objects that are not there are
`missing` (reason `not-restored`), and a name that was served stale when the state was saved is `stale` again with the
bytes it was served with. When the store is back the first round fetches what is missing, and the name is `ok`. A
restored object is checked like a fetched one (the listed size, the object limit): one that fails is failed locally
(`size-mismatch`, `too-large`) and is never served; nothing is requested for it.

**Layout** (version 1; the directory is created with mode `0700`, files are `0600`)

```text
<stateDir>/
  VERSION            "semaphore-manifest-state 1": marks the directory as ours
  LOCK               exclusive advisory lock (flock)
  manifest.json      the ETag, the manifest, the checksum and size of each object, the stale names, and a digest of all of it
  objects/<sha256>   one file per distinct content, named by its checksum
```

**Guarantees**

- Crash safe. A write goes to a temporary file in the same directory, is fsynced, renamed over its name, and the
  directory is fsynced. The order is: object files, fsync of `objects/`, the manifest (temporary file, fsync,
  rename), fsync of the directory, and only then the garbage collection. A manifest on disk never names an object
  that was not durable. A crash or a power loss at any step leaves the old state or the new one, never a mix: the
  tests cut the power after every file operation against a model that drops whatever was not fsynced.
- Temporary files. Every temporary file has a random name (`.tmp-` and exactly 16 lower case hex digits, one
  definition used both to create and to recognise them: `.tmp-abc`, a name of another length or a decimal name such as
  `.tmp-1`, which never shipped, belong to somebody else and are never removed or accepted as ours) and is created exclusively,
  with up to 8 tries for a free name: two writers on one directory never share a temporary name, and a file is never
  removed to make room for another. The leftovers of a crashed writer are removed by that pattern, after a save,
  only where the lock gives this store the directory; without a lock another writer may be in the middle of a write,
  so its temporary files are not ours and stay (they are never read, and a crash leaves at most a few). Names that do
  not match the pattern stay always.
- Nothing unverified is served. On load the manifest copy must match its digest and every object its sha256 and
  its recorded size. A file that fails is ignored on its own: a bad object costs one GET, a bad manifest copy a
  cold start.
- Reads are bounded. A name that is not a regular file (a FIFO, a device, a directory, a symbolic link) is never
  opened. A manifest copy above `MaxManifestBytes` (with the base64 allowance), an object above its recorded size
  or `MaxObjectBytes`, and lists above `MaxObjects` are discarded without being read.
- Cost proportional to the change: an object that is already on disk is not written again, and an unchanged
  snapshot writes nothing. One changed object is two file writes (the object and the manifest).
- Bounded disk use. `MaxBytes` (default 1 GiB, the default `maxTotalBytes`) bounds the objects, the manifest copy
  comes on top. The current objects come first, then the stale ones that still fit. When the current objects alone
  do not fit, the stale ones are dropped first and the current ones are kept in this order: the objects already on
  disk that the snapshot still needs come first, and the remaining budget goes to the new objects, smallest first
  (ties by checksum). This favours not losing what a restart can already restore over the count of contents: a
  cached 10-byte object is kept ahead of two new objects that total 10 bytes. "On disk" is what a listing of
  `objects/` finds as regular files named by a checksum when the write begins (the name and the type are trusted,
  not the content: a wrong file is caught by its checksum at restore, and every write that is not known to be
  durable puts the object there again), also after a failed write. Such a write is a partial save: it is counted
  as its own result (`partial`), not as an error.
- The order of a write never changes: the lock is held, then the `VERSION` file is validated, then the destinations
  are checked, then the budget is applied, then the objects are written, then the manifest, and last what the
  manifest does not reference is removed. A directory whose `VERSION` is foreign or unreadable (checked on every
  write, on the live store and after a reopen) is left exactly as it is: nothing is probed, removed or written, and
  the save fails with reason `unsafe`.
- Polling never waits for the disk. `Save` only queues the snapshot; one background writer writes the latest one
  (latest wins), so a slow or full disk delays nothing. A failed write (full disk, I/O error, permission) is
  reported in the status and the metrics, the next snapshot tries again, and polling goes on. The result of a
  write that ends after a round reaches `Status` one round late.
- Self-healing. Before each write the store lists `objects/` and the directory and checks the type of every file it
  trusts (a regular file): a vanished object or manifest copy, or a name that became a directory or a symbolic link,
  is written again even when the new state adds no checksum, on a store that stays open as well as after a restart.
  After ANY failed write the next write opens the directory again (a new lock, the symbolic links checked again)
  and forgets what it believed about the disk, then rebuilds the layout and writes every object again. A reopen
  that fails is retried by the next write. This is the safer rule: whatever the error was, nothing is trusted that
  the failed write may have left half done.
- Any write that does not complete, whether it failed or was rejected as unsafe, makes the next write open the
  directory again by path: a rejected directory that was moved aside and replaced is left alone, and the
  replacement is used; one left in place stays rejected on every write and is never modified.
- A directory under the name of an object or of the manifest copy is replaced when it is empty. One with content is
  never removed, and a symbolic link is replaced itself (a rename never follows it): the object is left out of the
  save before the budget is applied, so it never takes the place of an object that could be kept; the save is
  partial. For the manifest copy the save fails with reason `unsafe`.
- A state directory that `Open` creates, with its parents, is made durable at once (the parent of each created
  directory is flushed), so the first save survives a power loss. A flush that fails (not for lack of permission)
  is remembered by the opener (`OSFS` value), that is by the running store, not by the process or the disk: the
  path exists afterwards, but the next `Open` with the same opener flushes what is left, until it works, and fails
  with `ErrParentSync` meanwhile (`New` tries once more itself; `parent-sync` in the disabled status,
  `ParentSync = pending` while a save waits for it). A fresh process cannot know, and treats an existing path as
  flushed. A parent that cannot be opened for the flush because of permissions (write and search, no read) is
  skipped: the directory is a cache, so persistence stays on, the relay logs once at warn level with the path, and
  `State.ParentSync` is `skipped`. After a power loss the directory entry itself may then be missing, and the relay
  starts as on a first run.
- `Flush` returns when the writer is done with a snapshot: written, failed or replaced. The failure is in the status.

**The directory must be ours.** Nothing is ever written to, changed in or deleted from a directory that is not.

| Sentinel | Meaning | What was done to the directory |
|---|---|---|
| `ErrUnsafe` | A symbolic link, a file, a world-writable directory, a directory the process cannot read, write or lock, a layout name that is a symbolic link, or a directory with no `VERSION` that holds anything other than our `LOCK` and `.tmp-<number>` files (a volume root with `lost+found`: use a subdirectory) | Nothing |
| `ErrForeignVersion` | `VERSION` names another layout version | Nothing: not read, not written, not deleted |
| `ErrLocked` | Another process or store holds the lock | Nothing |
| `ErrParentSync` | The flush of a parent of a directory that was just created failed (an I/O error): it is retried by the next `Open` | The directory was created |

- The owner of the directory does not matter: a Kubernetes `fsGroup` volume is owned by root and group-writable, and
  is accepted. A directory that is not world-writable and that the process can read, write and lock is accepted;
  its mode is never changed. A directory that does not exist is created with mode `0700`.
- Nothing is created in a directory before it is known to be ours. The open inspects the directory read-only first
  (no lock file, nothing written): a directory that is foreign from the start is refused there, and there is nothing to
  clean up. Only a directory that is ours or empty gets its lock file opened and locked, and it is inspected again
  under the lock. When that second inspection refuses, a lock file that this open created is removed (one that was
  there stays): on Unix while it is still locked and then released, and an opener that had the same file open
  checks that the file it locked is still the file at the path (same inode) and retries if it is not; on Windows,
  which cannot remove an open file and has no lock, it is closed first. A lock file that cannot be removed is logged
  at warn level and never replaces the reason of the refusal.
- Garbage collection removes only names this package writes (a checksum in lower case hex, `.tmp-` and hex), under a
  `VERSION` it wrote or recognised.
- Two processes on one directory by mistake: the second fails with `ErrLocked`. Where the system has no `flock`
  (not unix) there is no lock: two writers can delete each other's objects (each one's garbage collection removes
  the other's new object before its manifest is renamed). Nothing is corrupted, because every file is complete or
  absent and everything read back is verified, but the restore is then partial and the missing objects are
  fetched again.
- Windows (compile-checked, not run there: the test binary builds with `GOOS=windows`, nothing was run on Windows
  or under wine). Every rule that is expressed in POSIX terms lives in `platform_other.go`, with its Windows variant
  in `platform_windows.go`; the rules that every system can express stay where they are applied.

  | Rule | Linux and macOS | Windows |
  |---|---|---|
  | Directory flush (parents of a created directory, layout steps) | `fsync` of the directory | none: a directory handle cannot be flushed; no guarantee that a directory entry survives a power loss, a restart reads what is there, verifies it and fetches what is missing |
  | World-writable directory refused | mode bit `0002` | skipped: Go reports the bits from attributes (`0777`), not from the access list, so the rule cannot be expressed |
  | Owner of the directory | not checked (a root-owned group-writable volume is fine) | not checked |
  | Exclusive lock (`ErrLocked`) | `flock` | none (`golang.org/x/sys` is not a direct dependency, so there is no `LockFileEx`): two writers can delete each other's objects, the restore is then partial, never corrupt. Temporary files have random names created exclusively, so two writers never share one, and the leftovers of a crashed writer are not removed (see below) |
  | Full disk / read-only errors | `ENOSPC`, `EROFS` | the same, plus `ERROR_DISK_FULL`, `ERROR_HANDLE_DISK_FULL`, `ERROR_WRITE_PROTECT` |
  | Symbolic link or reparse point as the directory or a layout name | refused | refused (symbolic links, and junctions where Go reports them as irregular) |
  | Only a regular file is read, names cannot leave the directory (`os.Root`) | yes | yes |
  | Write: temporary file, fsync, close, rename over the name | yes, and the rename is atomic | files are closed before the rename and `os.Rename` replaces the destination, but Go's contract does not promise atomicity there: a crash in the middle of a save can leave a manifest copy or an object file that fails its digest or checksum, and the restore discards it (a bad manifest copy is a cold start, a bad object costs one GET) |

  So on Windows the protections that apply are: the checks of type and name, temporary-file replacement with digest and
  checksum verification, the foreign-directory rules (`VERSION`, foreign files, symbolic links), and the
  bounded reads. Not applied: directory durability, the lock, the removal of a crashed writer's temporary files, and the
  world-writable rule.

**Caller policy.** `statedir.OpenOrDisable` is the constructor the relay uses. On `ErrUnsafe`, `ErrLocked`,
`ErrForeignVersion` (or any other failure to open) the relay runs without a state directory: it never fails to
start. The helper logs once at error level and returns hooks that only report the refusal: the status has
`State.Restore = "disabled"` and `State.DisabledReason` (`unsafe`, `locked`, `foreign-version`, `parent-sync`, `error`), and the
restore metric shows `result="disabled"`.

## Metrics

All series carry the `connection` label (a constant label, so several connections register on one registry). The
collector reads the reader status at scrape time. There is deliberately no per-object or per-flag series: the
number of series would grow with the manifest (up to 20000 objects) and with every flag. Per-object detail
(name, state, reason, checksum) is in the status. Series by closed sets (state, result) are the only ones.

| Series | Type | Labels | Meaning |
|---|---|---|---|
| `semaphore_manifest_generation` | gauge | `channel` | Generation of the held manifest (absent before the first accepted one) |
| `semaphore_manifest_last_sync_timestamp_seconds` | gauge | | Unix time of the last poll that confirmed the held manifest; 0 when none |
| `semaphore_manifest_polls_total` | counter | `result`: `not_modified`, `updated`, `not_found`, `too_large`, `error` | Manifest polls |
| `semaphore_manifest_objects` | gauge | `state`: `ok`, `stale`, `missing` | Objects of the held manifest |
| `semaphore_manifest_object_failures` | gauge | | Objects that are not `ok` |
| `semaphore_manifest_backoff_failures` | gauge | | Consecutive failed polls |
| `semaphore_manifest_backoff_wait_seconds` | gauge | | Wait planned before the next poll |
| `semaphore_manifest_backoff_objects_waiting` | gauge | | Failed objects waiting for their retry |
| `semaphore_manifest_state_saves_total` | counter | `result`: `ok`, `partial`, `error` | Writes of the state directory (only with one); `partial` is a write that kept only part of the objects (byte budget) |
| `semaphore_manifest_state_restore_total` | counter | `result`: `restored`, `partial`, `empty`, `failed`, `disabled` | Restore at start (with a state directory, or `disabled` when it was refused) |

They are not registered by `NewMetrics`: the relay registers them when it runs the reader, so the output of the
running relay does not change before that.

## Operations: a replica is behind

1. `semaphore_manifest_last_sync_timestamp_seconds`: how long ago did this replica last confirm the manifest? Compare
   `semaphore_manifest_generation` with the other replicas and with the published manifest.
2. `semaphore_manifest_polls_total{result="error"}` rising and `semaphore_manifest_backoff_failures` above 0: the
   store or the network is the problem. The wait grows to 5 min, and the replica serves what it has. Check
   `semaphore_s3_requests_total` by `result` (`throttled`, `client_error`, `server_error`, `error`).
3. `result="not_found"`: the manifest key is gone or wrong.
4. Polls are healthy but `semaphore_manifest_object_failures` is above 0: an object is `stale` or `missing`. The
   status names it with its reason (`missing`, `checksum-mismatch`, `denied`, ...). `semaphore_manifest_backoff_objects_waiting`
   tells that retries are scheduled; a new generation tries them at once.
5. A replica that restarted during an outage: `semaphore_manifest_state_restore_total` shows `restored` (or `partial`),
   and the generation is the saved one until the store answers.
6. `semaphore_manifest_state_saves_total{result="error"}` rising: the state directory cannot be written (disk full,
   permissions). Polling is not affected; a restart would be a cold start or serve an older state.
7. A rejected manifest (`invalid`, `lower-generation`, `generation-conflict`, `channel-mismatch`) is not a failed poll:
   look at the manifest outcome in the status, not at the poll results.

## Request metric

`semaphore_s3_requests_total{connection,operation,result}` counts every attempt of every S3 request of a connection:

- `operation`: `get`, `list`, `head`, `put`, `delete` or `other`
- `result`: `ok`, `not_modified`, `not_found`, `throttled`, `client_error`, `redirect`, `server_error` or `error`

A request retried after a `503` counts twice: one poll of the reader, two requests handed to the transport, two observations and
two counts (a test with the retries of the SDK on checks it). The counter is fed by the same observer, so the
independent count in the tests is a counting `RoundTripper` under the SDK. It counts the requests the SDK hands to the
transport (a refused connection is one), not packets: a replay by the Go transport of an idempotent request that got no
response is below it and is not counted (see [Connections](connections.md)). A `304` is `not_modified`, never an error. The only labels are the
connection name and the two closed sets: there is no per-flag or per-object series. With the manifest reader the
steady state is one `get`/`not_modified` per interval per connection.

See also [Connections](connections.md).
