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
- Only regular files are read. A symbolic link, a named pipe, a device or a socket is an error that names it: nothing is
  followed and nothing that could block is opened.
- A file larger than 16 MiB is refused from its size, before it is read; so is an empty document (an object must have at
  least one byte).
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
| `--replace-invalid-manifest` | Replace a manifest in the bucket that cannot be read, is invalid or is over the limits. Needs `--generation`. See [Recovery](#recovery-from-an-invalid-manifest). |
| `--output`, `-o` | `text` (default) or `json`. |

With `--output json` a failure writes one JSON object on stdout and exits with the usual code:

```json
{"error": {"code": "invalid", "message": "...", "details": ["bad.variations: is required"]}}
```

`code` is `usage`, `failure`, `invalid`, `conflict`, `transport` or `bad-state`; `details` lists the individual problems
(never null, never a secret). `publish verify` is the exception: a failed verification prints its report, which is the
JSON document, and exits with 5. Text mode is unchanged: the error goes to stderr.

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

1. Everything is validated first, with the `controlplane` `Validate...` functions (manifest, flag files, flag-set documents
   taken together, source references, duplicate names and API keys across documents, object limit), the
   [limits](#limits) and the [engine check](#what-is-checked-in-a-flag-file). A failure writes nothing and sends nothing.
2. The current manifest is read (one `GetObject`) with its ETag.
3. Each object gets its content-addressed key `objects/sha256/<2 hex>/<hex>.<ext>` (after the prefix). A `HeadObject` checks
   it; an absent object is created with `PutObject` and `If-None-Match: *` (and its sha256, so that S3 verifies the upload).
   An existing object must have the same size and the same sha256: when the store reports no sha256 or a composite one, the
   object is read and hashed. A mismatch is an error (exit 5); an object is never overwritten, and never accepted on its size
   alone. Up to 8 objects are handled at a time; the result does not depend on the order of completion, and the first
   failure cancels the others.
4. The manifest is written last, alone: `If-Match: <ETag read in step 2>`, or `If-None-Match: *` for the first manifest.

Guarantees:

- A reader never sees a manifest that lists an object that is not there: objects are written first and are immutable.
- Two publishers cannot both win: one conditional write succeeds, the other gets a conflict (exit code 3) and changes nothing
  visible. A `409 ConditionalRequestConflict` and a manifest deleted in between count as a conflict too. After a failed
  condition the manifest is read once: if it is the one we tried to write, an earlier attempt of ours committed (the SDK
  retried a request whose answer was lost) and that is success.
- The generation strictly increases. A lower or equal `--generation` is refused.
- A publish is safe to repeat after any failure.
- Re-publishing identical documents writes no manifest, not even a new generation, but the listed objects are checked, and
  an object that is missing is uploaded again (the output says so). `--generation` higher than the one in the bucket writes
  a new generation anyway.
- A missing bucket is an error of the store (exit 4), never "no manifest" and never a conflict.
- No `LIST` and no `DELETE` is ever sent. After an interrupted publish some objects may exist that no manifest lists: they are
  harmless (immutable, unreferenced, and reused by the next publish of the same content). Old and orphan objects are not
  collected yet; garbage collection is a later change.

## Limits

The reader rejects more than the format does, so the publisher refuses the same before any call (exit 2, naming the object):

| Limit | Value |
|---|---|
| Size of one object | 1 byte to 16 MiB |
| Sum of the sizes of the distinct objects | 1 GiB |
| Marshalled manifest | 16 MiB |
| Entries in the manifest | `controlPlane.manifest.maxObjects` of the bootstrap file in `--config` mode, else 20000 |

## What is checked in a flag file

Beyond the structural rules of `controlplane.ValidateFlags`, every flag file goes through the engine of the relay: the same
decoder (`ConvertToFlagStruct`), `DTO.Convert` and `IsValid` that the relay applies when it loads a flag file and that
`semaphore-cli lint` applies. That compiles the targeting queries and rejects, for example, a query that does not parse, a
rule that names no variation or percentage, a variation that is not declared, and percentages that sum to zero. The error
names the file and the flag. (The engine accepts percentages that do not sum to 100; so does the publisher.)

## Keys, connection name and permissions

- The `key` of every object in the manifest is the absolute key in the bucket, `--prefix` included. The prefix is applied by
  the publisher only; the relay reads the key as it is and must not add a prefix.
- The `connection` in the manifest must be the control-plane connection name of the relay's bootstrap file. `--config`
  mode takes it from the file and is recommended; in explicit mode `--connection` must equal that name.
- The identity needs `s3:GetObject` and `s3:PutObject` on the bucket. Add `s3:ListBucket` on the bucket: S3 answers a request
  for a missing key with `403 AccessDenied` instead of `404 NoSuchKey` when the caller may not list the bucket (this is the
  documented behaviour of S3), and a 403 makes the first publish of a channel and the checks for absent objects fail.
- `caBundleFile` of a connection is not available in `--config` mode yet (the bootstrap file has no such setting): the system
  roots are used.

## Recovery from an invalid manifest

A manifest in the bucket that cannot be read, is invalid or is over the current limits blocks the channel: `publish` and
`verify` report it (exit 5) and nothing overwrites it. To replace it on purpose:

```shell
semaphore-cli publish flags-repo --config bootstrap.yaml --replace-invalid-manifest --generation 42
```

`--generation` is required (pick one higher than any generation a relay may have seen). The write still uses `If-Match` on
the ETag that was read, so a manifest that someone wrote in between is not overwritten (exit 3). The flag has no effect when
the manifest in the bucket is valid.

## Verify

```shell
semaphore-cli publish verify --config /etc/semaphore/bootstrap.yaml
```

Reads the manifest and every listed object of the connection (reads only) and checks the limits, the size, the sha256, the
content of each document and that every flag-set source resolves, the flag-set documents taken together. Problems are listed per object with a stable code: `missing`, `unreadable`,
`size-mismatch`, `checksum-mismatch`, `invalid-content` (or the code of a validation problem such as `unresolved-source`),
`manifest-invalid`. Objects of other connections are listed as skipped. Same `--output` and target flags as `publish`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success (also `no change` and a dry run) |
| 1 | Failure with no more specific code, bad usage, unreadable file |
| 2 | Invalid input: a document, a file that is not regular or is too large, a limit, the source layout, the bootstrap file, or a generation that does not increase |
| 3 | Conflict: another publisher changed the manifest first. Nothing visible changed; run again |
| 4 | A call to the store failed (network, access, throttling). Safe to repeat |
| 5 | The bucket holds a bad state: `verify` found problems, there is no manifest to verify, or the manifest (invalid, over the limits) or an object does not match |

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

Rollback and history, channels and `promote`, signatures, `--manifest-only`, multiple buckets in one publish and garbage
collection of old objects are separate changes.
