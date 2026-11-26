# Publish

`semaphore-cli publish` is the reference publisher of the [control plane documents](../go-module/controlplane.md): it reads a
directory of flag files and flag-set documents, builds the manifest and writes both to S3 with the
[`s3publish`](../go-module/s3publish.md) package, so that a relay polling the manifest never sees a half-written state.
An external control plane uses the same package.

## Source directory

One document per file. The kind comes from the directory, the object name from the file name, the format from the extension.

```text
flags/<name>.yaml|yml|json      a flag file (kind flags)
flagsets/<name>.yaml|yml|json   a flag-set document (kind flagSets)
```

- Names match `^[a-z][a-z0-9-]{0,62}$` and are unique in the whole tree (a flag file and a flag-set document cannot share one).
- Flag-set documents name flag files in `sources` (the object names).
- Names that start with a dot are ignored. Any other file or directory is an error that names it, so a typo is not skipped.
- A directory that holds no document is an error.
- A directory named `verify` is taken as the subcommand: write `./verify`.

## Where to publish

Either the control-plane connection of a [bootstrap file](../relay-proxy/bootstrap-config.md), or explicit flags. They do not mix.

| Flag | Meaning |
|---|---|
| `--config`, `-c` | Bootstrap file: `controlPlane.connection` gives the bucket, endpoint, region and credentials, `controlPlane.manifest.key` and `channel` the manifest, `maxObjects` the limit. Secrets are read from `fromEnv` and `fromFile` references. |
| `--bucket`, `--region` | Without `--config`. Both are required. Credentials come from the AWS default chain. |
| `--endpoint` | Without `--config`: an emulator or S3 compatible store (path-style addressing is used). |
| `--channel` | Without `--config`. Default `stable`. |
| `--connection` | Without `--config`. The connection name written in the manifest. Default `control`. |
| `--manifest-key` | Without `--config`. Default `<prefix>manifests/<channel>.yaml`. A key ending in `.json` is written as JSON. |
| `--prefix` | Both modes. Put before every object key (and before the default manifest key). |

## Publish

```shell
semaphore-cli publish ./flags-repo --config /etc/semaphore/bootstrap.yaml
```

| Flag | Meaning |
|---|---|
| `--dry-run` | Validate and compute the plan: the same reads, no write. |
| `--generation N` | The generation to write. Default: the one in the bucket plus one (1 for the first manifest). It must be higher than the one in the bucket. |
| `--output`, `-o` | `text` (default) or `json`. |

Example output:

```text
published generation 1 (was 0) to manifests/stable.yaml, channel stable
objects: 2 uploaded, 0 already present, 225 bytes uploaded
requests: 1 get, 2 head, 3 put
manifest etag: "c9d96062-3"
  uploaded      checkout  objects/sha256/85/8500b7b2...c9f7.yaml (96 bytes)
  uploaded      checkout-sets  objects/sha256/c4/c4ef2631...eb5a.yaml (129 bytes)
```

The output lists names, keys, sizes and counts. It never prints API keys, credentials or document content.

## Upload protocol and guarantees

1. Everything is validated first with the `controlplane` `Validate...` functions (manifest, flag files, flag-set documents,
   source references, duplicate names and keys, object limit). A failure writes nothing.
2. The current manifest is read (one `GetObject`) with its ETag. Same documents already there: `no change`, nothing is written.
3. Each object gets its content-addressed key `objects/sha256/<2 hex>/<hex>.<ext>`. A `HeadObject` checks it; an absent object
   is created with `PutObject` and `If-None-Match: *` (and its sha256 so that S3 verifies the upload). An existing object with the
   same size is success; one with another size or sha256 is an error. An object is never overwritten.
4. The manifest is written last: `If-Match: <ETag read in step 2>`, or `If-None-Match: *` for the first manifest.

Guarantees:

- A reader never sees a manifest that lists an object that is not there: objects are written first and are immutable.
- Two publishers cannot both win: one conditional write succeeds, the other gets a conflict (exit code 3) and changes nothing
  visible. A `409 ConditionalRequestConflict` and a manifest deleted in between count as a conflict too.
- The generation strictly increases. A lower or equal `--generation` is refused.
- A publish is safe to repeat after any failure: objects are immutable and unreferenced until the manifest is written, and a
  repeat after a manifest write whose answer was lost finds its own manifest and writes nothing.
- Re-publishing identical documents writes nothing, not even a new generation. `--generation` higher than the one in the bucket
  writes a new generation anyway.
- No `LIST` and no `DELETE` is ever sent. Old objects stay in the bucket; garbage collection comes later.

## Verify

```shell
semaphore-cli publish verify --config /etc/semaphore/bootstrap.yaml
```

Reads the manifest and every listed object of the connection (reads only) and checks the size, the sha256, the content of each
document and that every flag-set source resolves. Problems are listed per object with a stable code: `missing`, `unreadable`,
`size-mismatch`, `checksum-mismatch`, `invalid-content` (or the code of a validation problem such as `unresolved-source`),
`manifest-invalid`. Objects of other connections are listed as skipped. Same `--output` and target flags as `publish`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success (also `no change` and a dry run) |
| 1 | Failure with no more specific code, bad usage, unreadable file |
| 2 | Invalid input: a document, the manifest, the source layout, the bootstrap file, or a generation that does not increase |
| 3 | Conflict: another publisher changed the manifest first. Nothing visible changed; run again |
| 4 | A call to the store failed (network, access, throttling). Safe to repeat |
| 5 | The bucket holds a bad state: `verify` found problems, there is no manifest to verify, or the manifest or an object does not match |

## End to end

```shell
mkdir -p flags-repo/flags flags-repo/flagsets
cat > flags-repo/flags/checkout.yaml <<'YAML'
checkout-new:
  variations: {"on": true, "off": false}
  defaultRule: {variation: "off"}
YAML
cat > flags-repo/flagsets/checkout-sets.yaml <<'YAML'
apiVersion: semaphore/v1
kind: FlagSet
flagSets:
  - name: checkout
    sources: [checkout]
    apiKeys: [8d3b5f0e-6a4f-4c8e-9c3e-2f2f5d1a7b10]
YAML

semaphore-cli publish flags-repo --config bootstrap.yaml --dry-run   # plan, no write
semaphore-cli publish flags-repo --config bootstrap.yaml             # generation 1
semaphore-cli publish flags-repo --config bootstrap.yaml             # no change, nothing written
semaphore-cli publish verify --config bootstrap.yaml                 # exit 0
```

> [!WARNING]
> API keys in flag-set documents are plain strings until dynamic keys exist. Protect the bucket with access control and
> encryption at rest; the publisher never prints or logs them.

## Not yet

Rollback and history, channels and `promote`, signatures, `--manifest-only`, multiple buckets in one publish, flag-object
validation with the relay's engine (`flagcheck`) and garbage collection of old objects are separate changes.
