# Segment

`semaphore-cli segment` loads and edits the members of a [dynamic segment](../configure-flags/segments-dynamic.md) in its
Valkey or Redis store. The relay only reads membership; this is the write side.

- The segment is named by its published definition (`--segments` and `--segment`): the key, the layout, the connection
  and the version key come from it. Nothing that the definition says is typed again.
- The connection comes from the [bootstrap file](../relay-proxy/bootstrap-config.md) (`--config`, or `SEMAPHORE_CONFIG`),
  with its secrets resolved from `fromEnv` and `fromFile`. The key prefix, the `normalize` setting and the longest id
  (`segments.resolver`) are read from it too, so the tool writes what the relay looks up.
- `--segments` is a file (YAML or JSON) of kind `Segments`. Reading the published document from the bucket comes with the S3 source.

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

- Each id is normalised as the relay normalises a lookup (`normalize` of the bootstrap file) and stored as the relay asks
  for it. The functions are shared with the relay (`resolver.NormalizeID`, `redissegment.DefaultKey`), so the two sides
  cannot drift. There is no hashing: the member is the normalised id.
- A line that is not an id is rejected and counted, never printed: `empty`, `tooLong` (a line over `--max-line-bytes`, default 64 KiB,
  or an id over `segments.resolver.maxIdBytes`), `invalid` (not valid UTF-8 under `trimLower`) and `column` (CSV). The report gives the line counts only.
- `--dry-run` reads and validates the input and writes nothing.
- An input with no valid id is an error (exit 2), because swapping it in would wipe the segment. `--allow-empty` accepts it.
- `--batch-size` (default 1000, at most 100000) is the ids of one `SADD`; `--temp-ttl` (default 1h) is the life of the temporary key.
- The `keys` layout is refused (exit 2): it has no single key to swap, and any other protocol shows the relay a half built segment. Use `add` and `remove` for it.

The steps, in this order:

1. Remove the temporary keys that a killed run of the same segment left.
2. `SADD` the batches to the temporary key. Each batch refreshes its `PEXPIRE`.
3. `MULTI`: `RENAME live old`, `RENAME temporary live`, `PERSIST live`, `PEXPIRE old`; `EXEC`. A reader sees the whole old set or the whole new set.
4. `SET` the version key (when the definition has `version.key`) to the time of the swap, so that the relay sees the change. The swap comes first, the bump second.
5. `UNLINK old`, which frees the old set in the background. A server without `UNLINK` (Redis before 4.0) gets `DEL`, and the report says so.

Interruption: a cancelled context, a lost connection or SIGINT/SIGTERM before step 3 leaves the live set untouched and
removes the temporary key. A process that is killed leaves a key that expires after `--temp-ttl` and that the next run removes.
A failure after step 3 is reported as such (the new set is live); the old set expires in `--temp-ttl` if step 5 failed.
Run one import of a segment at a time: the temporary key has one name for each segment.

## Add and remove

```shell
semaphore-cli segment add --config relay.yaml --segments segments.yaml --segment vip alice@example.com bob@example.com
semaphore-cli segment remove --config relay.yaml --segments segments.yaml --segment vip --from remove.txt
```

Small edits of the live key (`SADD` and `SREM` for the `set` layout, `SET NX` and `UNLINK` of `<key><id>` for `keys`), then the version bump.
The ids are normalised like an import. Without `--from`, the ids are the arguments.

## Stat

```shell
semaphore-cli segment stat --config relay.yaml --segments segments.yaml --segment vip
```

Prints whether the segment exists, its members (`SCARD`; for `keys`, counted by `SCAN` up to `--scan-limit`, printed as `at least N` when it is hit),
the version token and the ttl of the set when it has one.

## Output and exit codes

- Counts only: no id is printed, logged (`--debug` logs the steps to stderr) or put in an error.
- 0 done, 1 failure, 2 invalid input (empty input, the `keys` layout, bad options), 4 the store failed.

## Data contract

Key layouts (see [Redis](../configure-flags/segments-redis.md) and [Valkey](../configure-flags/segments-valkey.md)):

| Layout | Key | Members |
| --- | --- | --- |
| `set` (default) | `key` of the definition, or `<keyPrefix>{<name>}` (prefix `semaphore:segment:`) | one set |
| `keys` | `key` of the definition (a prefix), or `<keyPrefix><name>:` | one key `<prefix><id>` for each member |

- The default set key holds the segment name in a hash tag. The temporary keys keep the tag of the live key (`<key>:import:new`, `<key>:import:old`), or, for a key without a tag, wrap the key in one (`{<key>}:import:new`), so that they are in the cluster slot of the live key and the swap works on a cluster. A key whose tag cannot be shared is refused.
- The version key is a string: the time of the last change. Keep it outside the prefix of a `keys` segment, or `stat` counts it.
- The temporary key has a ttl from its first batch (`--temp-ttl`, refreshed by each batch). The live key has no ttl: `PERSIST` removes the one that `RENAME` carries over.

ACL of the relay (read only) and of the writer, side by side. The writer line lists every command the tool sends:

```text
ACL SETUSER relay on >secret ~semaphore:segment:* resetchannels -@all +ping +hello +client +smismember +exists +get +sscan
ACL SETUSER segwriter on >secret ~semaphore:segment:* ~audiences:* resetchannels -@all +ping +hello +client +cluster|slots +cluster|shards +role +multi +exec +sadd +srem +set +get +rename +persist +pexpire +unlink +del +scard +pttl +scan
```

- `+cluster|...` and `+role` are for a cluster client and for `stat` of a `keys` segment on a cluster. `+del` is for the fallback only. Adapt the key patterns to your keys.
- Lazy free: `UNLINK` frees in a background thread whatever the settings. The `RENAME` that puts the new set live never frees a set, because the old one has been moved to its own key first. Set `lazyfree-lazy-user-del yes` anyway if other tools use `DEL` on these keys.
- Memory: during a rebuild the store holds the old set, the new set and the old set until `UNLINK` has run. The peak is about old plus new. Plan for it.
- A batch is one command of at most `--batch-size` members, so no command runs long and no other command waits for the import. There is no `KEYS`, no `DEL` of a large key and no script over the whole set.

## Measured

On a commodity 4-core / 8-thread Linux machine with a Valkey 8 container (`task segment-bench`):

- Replacing a set of 2,000,000 ids by another set of 2,000,000 ids took about 1.3 s.
- The server memory was 82 MiB with the old set and peaked at 165 MiB during the rebuild (old plus new).
- The longest wait of a `PING` sent in a loop during the import was 2 ms.
- With 200,000 ids, readers that asked for ids of both sets in one lookup never saw a part of a set, on a standalone store and on a 3-node cluster, with valkey-go and with go-redis.
