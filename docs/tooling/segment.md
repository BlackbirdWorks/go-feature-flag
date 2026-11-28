# Segment

`semaphore-cli segment` loads and edits the members of a [dynamic segment](../configure-flags/segments-dynamic.md) in its
Valkey or Redis store. The relay only reads membership; this is the write side.

- The segment is named by its published definition (`--segments` and `--segment`): the key, the layout, the connection
  and the version key come from it. Nothing that the definition says is typed again.
- The connection comes from the [bootstrap file](../relay-proxy/bootstrap-config.md) (`--config`, or `SEMAPHORE_CONFIG`),
  with its secrets resolved from `fromEnv` and `fromFile`. The key prefix, the `normalize` setting and the longest id
  (`segments.resolver`) are read from it too, so the tool writes what the relay looks up.
- `--segments` is a file (YAML or JSON) of kind `Segments`. Without it, the command reads the segments objects that the manifest of the `controlPlane` of the bootstrap file lists, through the same object stores as the relay: the manifest, then each segments object, whose size and checksum must be the listed ones. A segment name that two objects define is an error, and so is a manifest with no segments object. Errors name the manifest key and the object (`manifest "m.json": objects[1] (people): key "...": checksum mismatch`).

```yaml
version: 2
connections:
  audiences:
    type: valkey
    addresses: ["valkey.internal:6379"]
    username: segwriter
    password: {fromEnv: SEGMENT_WRITER_PASSWORD}
segments:
  resolver:
    default: {normalize: trimLower}
```

```yaml
apiVersion: semaphore/v1
kind: Segments
segments:
  - name: vip
    connection: audiences
    mode: lookup
    version: {key: "audiences:vip:version"}
```

## Import

```shell
semaphore-cli segment import --config relay.yaml --segments segments.yaml --segment vip --from vip-ids.txt
cat ids.csv | semaphore-cli segment import --config relay.yaml --segments segments.yaml --segment vip --from - --format csv --column 2
```

Replaces the members of the segment by the ids of the input, one id for each line (`--format lines`, default) or one
column of a CSV line (`--format csv --column n`; a quoted field cannot hold a line break).

- The input may start with a UTF-8 byte order mark: it is dropped. Each id is normalised as the relay normalises a lookup (`normalize` and `maxIdBytes` of the bootstrap file, or the defaults of the relay when it has no `segments` section: none and 512) and stored as the relay asks
  for it. The functions are shared with the relay (`resolver.NormalizeID`, `redissegment.DefaultKey`), so the two sides
  cannot drift. There is no hashing: the member is the normalised id.
- A line that is not an id is rejected and counted, never printed: `empty`, `tooLong` (a line over `--max-line-bytes`, default 64 KiB,
  or an id over `segments.resolver.maxIdBytes`), `invalid` (not valid UTF-8 under `trimLower`) and `column` (CSV). The report gives the line counts only.
- `--dry-run` reads and validates the input and writes nothing. The report prints `duplicates: unknown` when a batch was sent again after a lost reply (the count of new members is then low).
- An input with no valid id is an error (exit 2), because swapping it in would wipe the segment. `--allow-empty` accepts it.
- `--batch-size` (default 1000, at most 100000) is the ids of one `SADD`; `--temp-ttl` (default 1h) is the life of the temporary key.
- The `keys` layout is refused (exit 2): it has no single key to swap, and any other protocol shows the relay a half built segment. Use `add` and `remove` for it.

A relay that is mid-pull during a swap can serve a mix of old and new members for up to one `version.interval`.

To replace a segment without a swap, see [a new key](#a-new-key-instead-of-a-swap).

## S3 source

```shell
semaphore-cli segment import --config relay.yaml --segment vip --from s3://ids-bucket/lists/vip.txt
semaphore-cli segment import --config relay.yaml --segment vip --from s3://ids-bucket/lists/vip/ --max-objects 200
```

`--from s3://<bucket>/<key>` reads one object. `--from s3://<bucket>/<prefix>/` (a slash at the end) reads every object under the prefix, in key order, as one input: a line break is put between two objects, so the last line of one never joins the first of the next. The formats, the normalisation, the limits and the counts are the ones of a file.

- **Connection.** The S3 client is the one of the `s3` connection of the bootstrap file: credentials, region, endpoint and TLS come from it and there are no flags for them. `--source-connection <name>` names the connection; when the file has exactly one `s3` connection, that is the default (the control-plane connection counts, so a file with a control plane and a data bucket needs the flag). The bucket of the URL must be the `bucket` of the connection.
- **IAM.** The role needs `s3:GetObject` on the objects and `s3:ListBucket` on the bucket, limited to the prefix with the `s3:prefix` condition. A single object needs `s3:GetObject` only (the check of its ETag uses `HEAD`, which `s3:GetObject` allows).
- **Streaming.** An object is read with one `GET` and never held whole in memory; no temporary file is written. An object that is gzip (`Content-Encoding: gzip`, or a name that ends with `.gz`) is decompressed on the fly.
- **A changed source aborts the import before the swap.** Each `GET` carries `If-Match` with the ETag of the listing, so an object that was replaced after the listing fails the import. At the end of the input the listing is read again, and an object that was added, removed or replaced fails it too. A read error in the middle of an object, or a body shorter than announced, fails it. In each case the live set is untouched (exit 3 for a changed source, 1 for the others).
- **Bounds.** A listing is paginated and holds at most `--max-objects` objects (default 1000): more is an error (exit 2), never a part of the prefix. A prefix with no object is an empty input: an error unless `--allow-empty`.
- The import records the fingerprint of the source with the swap (see [Sync](#sync)).

## Sync

```shell
semaphore-cli segment sync --config relay.yaml --segment vip --from s3://ids-bucket/lists/vip/ --once
semaphore-cli segment sync --config relay.yaml --segment vip --from s3://ids-bucket/lists/vip/ --interval 5m
```

Keeps the segment in step with an object or a prefix: it checks the source and runs an import when the source is not the one that the live set came from.

- **Fingerprint.** The identity of the source is a SHA-256 over the key, ETag and size of each object: one `HEAD` for an object, the listing for a prefix. No content is read to compare. It is stored in `<key>:import:source`, in the slot of the set, by the same script that swaps the set: the swap and the record are one step, so a crash between them cannot skip a change or leave a record of a set that is not live. An import without a fingerprint (a file, standard input) removes the record, so the next sync imports again.
- **Rounds.** A round reads the stored fingerprint (one `GET`) and the source. Equal: `unchanged`, no write. Different, or no record (the first run): the source is imported. Another import holds the lock: `skipped`, the round does nothing and the next one tries again.
- **Failure.** A failed round (the source, the store or the import) keeps the previous set. It is retried at the next interval; after repeated failures the pause doubles, up to 16 intervals, and comes back to the interval after a good round.
- **Health.** Each round logs one line to standard error at info level with the outcome (`changed`, `unchanged`, `skipped` or `failed`), the counts and the duration. Warnings carry the error text. No id is ever logged.
- **Lifecycle.** The command runs until its context ends: the first SIGINT or SIGTERM stops the import in flight before its swap (or lets a swap that was sent finish), and a second signal ends the process at once. `--once` runs one round and exits with its result: 0 when the source was unchanged, imported or skipped, the exit code of the import when it failed.
- The import options of `import` apply (`--format`, `--column`, `--batch-size`, `--temp-ttl`, `--max-line-bytes`, `--allow-empty`, `--max-objects`, `--source-connection`).

Run it as a Kubernetes `CronJob` with `--once` (recommended: one short process, the schedule is the interval, a failure is a failed job with its exit code), or as a long-running process with `--interval` (default 1m).

```yaml
apiVersion: batch/v1
kind: CronJob
metadata: {name: segment-sync-vip}
spec:
  schedule: "*/5 * * * *"
  concurrencyPolicy: Forbid
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: Never
          containers:
            - name: sync
              image: semaphore-cli
              args: [segment, sync, --once, --config, /etc/semaphore/relay.yaml, --segment, vip, --from, "s3://ids-bucket/lists/vip/"]
```

## A new key instead of a swap

The other way to replace a segment: build the new list under a new key, then publish the definition with that key.

```shell
semaphore-cli segment import --config relay.yaml --segments segments-v2.yaml --segment vip --from vip-ids.txt   # key: audience:vip-v2
semaphore-cli publish ./control --config relay.yaml                                                              # ./control/segments/ now says key: audience:vip-v2
```

The relay reloads the definition and reads the new key: no version bump and no version key is needed. A snapshot segment pulls the new set at once on the reload, not after an interval; a lookup segment does not serve the answers that it cached for the old key. The old list stays where it was until you remove it, so going back is publishing the old key again.

Choose the in-place import when the key must not change and a short mixed read (see above) is fine. Choose the new key when readers must see the old list or the new one with no mix, or when you want to keep the old list for a rollback.

The protocol is safe by construction, not by narrow windows:

1. **Lock.** A script takes `<key>:import:lock` with the token of the run and a ttl when it is free, and answers yes again when it already holds this token, so a call whose reply was lost is sent again (three tries) and a lock that may have been taken is released by token when no answer comes. A second import of the segment fails at once (exit 3, "another import of this segment is running"). There is no `--force`: the lock of a dead run expires by its ttl (`--temp-ttl`, default 1h, at least 1s). Every batch refreshes the lock, and the lock is released at the end only if it still holds the token of the run.
2. **Temporary key.** `<key>:import:<token>:new`: the name holds the token of the run, so no other run can share it or recreate it. The run records the name in `<key>:import:last`; once it holds the lock, one script (that checks the lock token) removes exactly the key that the last run named, if a dead run left it, and records the new name. A run that does not hold the lock never touches those keys. This is a plain `GET` and `UNLINK`, not a `SCAN` (a scan of a slot is unbounded work; the recorded name is O(1)).
3. **Batches.** Each batch is one script call that checks the token of the lock, `SADD`s at most 1000 ids at a time, and refreshes the ttl of the temporary key and of the lock. A batch is refused if the lock is no longer the run's. Only the first batch may create the temporary key: a later batch that finds it missing (it expired or was removed) fails with "temporary key is gone" instead of recreating it, so the key always holds every batch or none. A batch whose reply was lost is sent again (a set insertion is idempotent); the count of new members can then be low, and the swap asks for at least that many.
4. **Conditional swap.** One script on the server, keys in one slot. It swaps only if the lock still holds the token of the run, the temporary key exists, and it holds at least the number of distinct ids that the run saw added. Then `RENAME live old` (with a ttl for `old`), `RENAME temporary live`, `PERSIST live`, and records the token in `<key>:import:committed` (ttl `--temp-ttl`) in the same step. If any condition fails, nothing changes: the live set is not moved, and the run exits non-zero (`the temporary key is gone`, `does not hold the members that were written`, or `the import lock is held by another run`, exit 3). An empty import (`--allow-empty`) is its own path: the script removes the live key, only if the lock is the run's.
   The swap is idempotent by the token: when its reply is lost (a timeout, a reset), the tool sends it again, up to three times with a short pause, and a second call of a swap that was applied answers "already swapped". If no answer ever comes, the command exits 6 with `swap state unknown: run semaphore-cli segment stat, then import again`: the live set may or may not be the new one, and the version was not bumped.
5. **Version bump**, when the definition has `version.key`: the swap comes first, the bump second, with three tries. If it still fails, the command exits 1 with `set replaced, version not bumped: run semaphore-cli segment bump`: that command bumps the version key alone (`segment bump --segment <name>`, same flags), so that the relays read the set again.
6. **`UNLINK old`** (skipped only for an explicit empty import), which frees the old set in the background. A server without `UNLINK` (Redis before 4.0) gets `DEL`; the report says so, and warns when the replaced set had more than 100,000 members, because `DEL` of a large set can block the server.

Interruption: a cancelled context, a lost connection or SIGINT/SIGTERM (a read of standard input that blocks ends with the signal; a second signal ends the process at once) before step 4 leaves the live set untouched. The cleanup of the temporary key and of the lock is best effort, with its own timeout: when it fails, the temporary key expires by its ttl, the lock expires by its ttl, and the next run removes the recorded key. A failure after step 4 is reported as such (the new set is live); the old set expires in `--temp-ttl` if step 6 failed.

## Readers during an import

- A relay that serves lookups answers from its cache until it sees the new version key: the bound is one poll interval of the version key (`version.interval` of the definition, or the default of the relay), plus the time of its cache entries that were filled before.
- A relay in snapshot mode that is in the middle of a scan of the set while the swap happens can hold a mixed set until its next poll sees the new version, one poll interval at most.
- The swap is one script execution on the server, and a lookup of the relay is one command: each answers from the whole old set or the whole new set, never from a part of one. A lookup that began before the script ended may answer from the old set.

## Add and remove

```shell
semaphore-cli segment add --config relay.yaml --segments segments.yaml --segment vip alice@example.com bob@example.com
semaphore-cli segment remove --config relay.yaml --segments segments.yaml --segment vip --from remove.txt
```

Small edits of the live key (`SADD` and `SREM` for the `set` layout, `SET NX` and `UNLINK` of `<key><id>` for `keys`), then the version bump.
The ids are normalised like an import. An edit of a set is refused while an import of the segment holds the lock (exit 3), and the check and the write are one step on the server, so an edit is never lost at the swap while the command says it worked. Edit the segment before or after the import. Without `--from`, the ids are the arguments; each argument is one id (an id with a line break or another control character is rejected and counted, as in a file). If a batch fails after it was sent (a lost reply may hide that it was applied), the run stops sending, waits a moment (200 ms) for the batch that may still be running, and bumps the version (on a context of its own, also when the command was cancelled), and the command exits non-zero with `partially or possibly applied: N of M ids confirmed`. When the store is slower than that moment, a batch can still land after the bump: run `semaphore-cli segment bump` once more.

## Stat

```shell
semaphore-cli segment stat --config relay.yaml --segments segments.yaml --segment vip
```

Prints whether the segment exists, its members (`SCARD`; for `keys`, distinct keys counted by `SCAN` up to `--scan-limit`, default 100,000, printed as `at least N` when it is hit; memory is bounded by that limit),
the version token and the ttl of the set when it has one.

## Bump

```shell
semaphore-cli segment bump --config relay.yaml --segments segments.yaml --segment vip
```

Writes a new version to the version key of the definition and nothing else (three tries). Use it when an import or an edit reports that the version was not bumped. A definition without `version.key` is refused.

## Output and exit codes

- Counts only: no id is printed, logged (`--debug` logs the steps to stderr) or put in an error.
- 0 done, 1 failure (also: the set was replaced and the version could not be bumped), 2 invalid input (empty input, the `keys` layout, bad options such as `--max-line-bytes` under 16, a prefix over `--max-objects`, a source URL or connection that cannot be resolved), 3 conflict (another import holds the lock, the source changed while it was read, or the swap condition did not hold: nothing changed), 4 the store failed, 6 the outcome is not known (no answer from the swap: look with `stat` before repeating).

## Data contract

Key layouts (see [Redis](../configure-flags/segments-redis.md) and [Valkey](../configure-flags/segments-valkey.md)):

| Layout | Key | Members |
| --- | --- | --- |
| `set` (default) | `key` of the definition, or `<keyPrefix>{<name>}` (prefix `semaphore:segment:`) | one set |
| `keys` | `key` of the definition (a prefix), or `<keyPrefix><name>:` | one key `<prefix><id>` for each member |

- The default set key holds the segment name in a hash tag. The keys of an import (`<key>:import:lock`, `<key>:import:last`, `<key>:import:<token>:new`, `<key>:import:<token>:old`) keep the tag of the live key, or, for a key without a tag, wrap the key in one (`{<key>}:import:...`), so that they are in the cluster slot of the live key and the scripts work on a cluster. A key whose tag cannot be shared is refused.
- The record of the source (`<key>:import:source`) is a string, the fingerprint of the input of the last import that had one. It is in the slot of the set and is written by the swap script; the writer ACL below covers it.
- The version key is a string: the time of the last change. Keep it outside the prefix of a `keys` segment, or `stat` counts it.
- The tool refuses (exit 2) a segment whose keys can meet those of another segment of the same document on the same connection: no key, version key or member prefix of another segment may be one of the keys that an import of the segment uses (`<key>:import:...`), and two segments may not share a key. It removes only keys that it created under its own token.
- The temporary key and the lock have a ttl from the first batch (`--temp-ttl`, refreshed by each batch). The live key has no ttl: `PERSIST` removes the one that `RENAME` carries over.

ACL of the relay (read only) and of the writer, side by side. The writer line lists every command the tool sends:

```text
ACL SETUSER relay on >secret ~semaphore:segment:* resetchannels -@all +ping +hello +client +smismember +exists +get +sscan
ACL SETUSER segwriter on >secret ~semaphore:segment:* ~audiences:* resetchannels -@all +ping +hello +client +cluster|slots +cluster|shards +role +eval +evalsha +get +set +sadd +srem +scard +exists +rename +persist +pexpire +pttl +scan +unlink +del
```

- The ACL line does not change for the S3 source and for sync: the sync reads the record with `get`, and the swap writes it with `set` and `del`, inside its script. The access to the bucket is the IAM role of the connection.
- `+eval` and `+evalsha` run the three scripts (batch, swap, unlock); the commands inside them (`get`, `set`, `sadd`, `scard`, `exists`, `rename`, `persist`, `pexpire`, `unlink`, `del`) are checked against the user too. `+cluster|...` and `+role` are for a cluster client and for `stat` of a `keys` segment on a cluster. `+del` is for the fallback only. Adapt the key patterns to your keys. A test makes a user from exactly this line and runs every command of the tool with it.
- Lazy free: `UNLINK` frees in a background thread whatever the settings. The `RENAME` that puts the new set live never frees a set, because the old one has been moved to its own key first. Set `lazyfree-lazy-user-del yes` anyway if other tools use `DEL` on these keys.
- Memory: during a rebuild the store holds the old set, the new set and the old set until `UNLINK` has run. The peak is about old plus new. Plan for it.
- Retries: the tool keeps the retry settings of the client libraries (go-redis retries a failed command up to three times by default; valkey-go retries reads only) and does not depend on them. Every write is safe to send twice: a batch is a set insertion, the swap and the unlock are idempotent by the token of the run, an edit is a set insertion or removal. The tool sends a batch or the swap again itself (three tries, 100 ms then 200 ms) when the reply is lost.
- A batch is one script call of at most `--batch-size` members (scripts add them 1000 at a time), so no command runs long and no other command waits for the import. There is no `KEYS`, no `SCAN` and no script over the whole set. `UNLINK` frees the old set in the background; the `DEL` fallback of a server without `UNLINK` can block while it frees a large set.

## Measured

On a commodity 4-core / 8-thread Linux machine with a Valkey 8 container (`task segment-bench`):

- Replacing a set of 2,000,000 ids by another set of 2,000,000 ids took about 1.3 s.
- The server memory was 82 MiB with the old set and peaked at 165 MiB during the rebuild (old plus new).
- The longest wait of a `PING` sent in a loop during the import was 2 ms.
- With 200,000 ids, readers that asked for ids of both sets in one lookup never saw a part of a set, on a standalone store and on a 3-node cluster, with valkey-go and with go-redis.
