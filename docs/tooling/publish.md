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
- A file larger than 16 MiB is refused from its size, before it is read. More files than the limit, or more than 1 GiB in
  distinct documents, stop the loading as soon as the limit is crossed (equal documents count once). An empty document is
  refused by the publisher (an object must have at least one byte).
- A directory that holds no document is an error.
- A directory named `verify` is taken as the subcommand: write `./verify`.

## Where to publish

Either the control-plane connection of a [bootstrap file](../relay-proxy/bootstrap-config.md) (the version 2 format, which the relay
does not read yet), or explicit flags. They do not mix.

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

With `--output json` a failure writes one JSON object on stdout and exits with the usual code; the final `error executing command`
line of the process goes to stderr, so stdout is one JSON document. A missing or extra argument and a flag that does not parse
are reported the same way, whatever the order of the flags (JSON mode is read from the raw arguments, the last `-o`/`--output` wins):

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
- Two publishers that write different manifests over the same one cannot both win: exactly one conditional write succeeds,
  the other gets a conflict (exit code 3) and changes nothing visible. A `409 ConditionalRequestConflict` and a manifest
  deleted in between count as a conflict too. After a failed condition the manifest is read once: if it is the whole
  manifest we tried to write (same channel, generation, entries and `createdAt`), an earlier attempt of ours committed (the
  SDK retried a request whose answer was lost) and that is success. Two publishers that write the identical manifest both
  succeed.
- The generation strictly increases. A lower or equal `--generation` is refused.
- After a conflict (exit 3) the command does not retry: you decide. With no `--generation`, running it again reads the
  winner's manifest and publishes over it with the next generation, so the last writer wins, by intent. With an explicit
  `--generation` it fails until the number is higher than the winner's.
- A transient store failure (a timeout, a 5xx) is safe to repeat: the repeat converges. A permanent one (access denied, a
  missing bucket) fails the same way again until the cause is fixed.
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

The error says where and what kind of failure it is, never what the file holds: the file, the flag key, the line, column or
byte the decoder gives, the kind of value found and the type expected, and the category of the engine's reason (for
example `invalid query`). It does not repeat a value, an API key or the text of a query, in text or in JSON output, even
when the decoder or the engine would quote them.

## Keys, connection name and permissions

- The `key` of every object in the manifest is the absolute key in the bucket, `--prefix` included. The prefix is applied by
  the publisher only; the relay reads the key as it is and must not add a prefix.
- A `--manifest-key` that equals the key of one of the objects (the key follows from the content) is refused before any
  call: the manifest would overwrite that object.
- The `connection` in the manifest must be the control-plane connection name of the relay's bootstrap file. `--config`
  mode takes it from the file and is recommended; in explicit mode `--connection` must equal that name.
- Permissions, from the calls the tool makes (all on the bucket of the connection):

  | Command | Calls | IAM actions |
  |---|---|---|
  | `publish` | `GetObject` (manifest, and objects that must be hashed), `HeadObject`, `PutObject` | `s3:GetObject` (also needed by `HeadObject`), `s3:PutObject` |
  | `publish verify` | `GetObject` | `s3:GetObject`, plus `s3:GetObjectVersion` for a manifest entry that pins a `versionId` |
  | both | | `s3:ListBucket`, see below |

  No `LIST`, `DELETE` or ACL call is made. `s3:ListBucket` is not called either, but it changes what S3 answers for a key that
  does not exist: with it S3 answers `404`, without it `403 AccessDenied` (documented S3 behaviour). The tool cannot tell that
  403 from a real access error, so it never guesses: a missing manifest or a new object without `s3:ListBucket` stops the
  publish with exit code 4 and a message that says `S3 also answers 403 for a key that does not exist when the identity lacks
  s3:ListBucket`. `verify` fails the same way (exit code 4, no report) when it cannot read the manifest, and reports an object that
  it cannot read as `unreadable` with the same note. Nothing is written and nothing is
  taken for "present". Grant `s3:ListBucket` for the first publish of a channel and for new objects.
- `caBundleFile` of a connection is not available in `--config` mode yet (the bootstrap file has no such setting): the system
  roots are used.

## Publishing over a manifest with a bad entry

An existing manifest that lists an entry the relay fails alone (a size of 0, a size over the limit) is a valid manifest: `publish`
writes the next generation over it with the usual `If-Match`, with no flag. `--replace-invalid-manifest` is only for a manifest
that is invalid as a whole (not readable, wrong header, too many entries, over the total size).

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
`invalid-entry`, `size-mismatch`, `checksum-mismatch`, `invalid-content` (or the code of a validation problem such as `unresolved-source`),
`manifest-invalid`. Objects of other connections are listed as skipped, and the text output says so. Same `--output` and target
flags as `publish`.

- Like the relay, `verify` fails some entries alone and rejects the manifest for the others (a test pins this table):

  | Problem of an entry | Classification |
  |---|---|
  | size 0 | `invalid-entry` of that entry; the others are checked |
  | size over the object limit (16 MiB by default) | `invalid-entry` of that entry; the others are checked |
  | key with a leading slash | `invalid-entry` of that entry; the others are checked |
  | negative size | `manifest-invalid`: nothing is read |
  | empty key | `manifest-invalid`: nothing is read |
  | bad checksum, bad name, unknown kind or format, any other rule of `ValidateManifest` | `manifest-invalid`: nothing is read |
  | not parseable, wrong header, too many entries, distinct objects over the total limit | `manifest-invalid`: nothing is read |

  In the first three rows the other entries are still read and checked; in the others nothing is read.
- A manifest entry with a `versionId` is read at that version; an absent version is `missing`.
- Flag files also go through the engine check (below), so a stored file that the relay would not load is `invalid-content`
  even when its size and checksum match. A flag file listed with `format: toml` is accepted as far as the structure goes
  and checked by the engine.
- The report lists problems in manifest order (entries are sorted by name), cross-document problems at the position of
  their entry; a problem that belongs to no entry comes last. "checked N objects" (`checked` in JSON) counts the objects
  whose bytes were read and compared with the listed size and checksum; an object that is missing or cannot be read is a
  problem and is not counted.
- A clean report is about the objects of the connection that were read. It does not say anything about the objects that
  are skipped.

## Streams

One rule holds for every `semaphore-cli` command:

- **stdout carries results only.** That is the command output and the reports, including a `publish verify` text report that
  lists problems. With `--output json` it is exactly one JSON value: the result, the `verify` report, or the error object.
- **stderr carries every diagnostic.** That is Cobra's usage and its own errors, and the final `error executing command` line of
  every command (an unknown flag or subcommand, a bad argument count, `lint`, `evaluate`, `config`, `publish`). With
  `--output json` the error object is on stdout and the final line is still on stderr, so no other text reaches stdout.

| Case | stdout | stderr | Exit code |
|---|---|---|---|
| Success, text | the result | nothing | 0 |
| Success, `-o json` | one JSON document | nothing | 0 |
| Failure, text | nothing | the diagnostics and the final line | see below |
| Failure, `-o json` | one JSON error object | the final line | see below |
| `publish verify` with problems, text | the report | the final line (`verification failed`) | 5 |
| `publish verify` with problems, `-o json` | the report (one JSON document, `ok: false`) | the final line | 5 |
| Unknown root flag or subcommand | nothing | Cobra's message, usage and the final line | 1 |
| Argument or flag error of `publish` or `config`, `-o json` | one JSON error object (`usage`) | the final line | 1 |

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success (also `no change` and a dry run) |
| 1 | Every other failure: bad usage (an unknown flag or subcommand, a bad argument count), an unreadable file, any command that defines no other code |
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
