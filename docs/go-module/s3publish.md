# Publish to S3 (`s3publish`)

`github.com/blackbirdworks/semaphore/controlplane/s3publish` is the upload protocol of the control plane. A control plane
service imports it next to [`controlplane`](controlplane.md) and gets "objects first, manifest last" with conditional
writes. [`semaphore-cli publish`](../tooling/publish.md) is built on it.

- It imports no `internal` package. It needs an `API`: the three calls `GetObject`, `HeadObject` and `PutObject`, which a
  `*s3.Client` of the AWS SDK for Go v2 implements.
- It never sends a `LIST` or a `DELETE`.

## Example

```go
client := s3.NewFromConfig(awsCfg) // your client, region, endpoint and credentials

pub, err := s3publish.New(client, &s3publish.Config{
    Bucket:     "acme-control",
    Channel:    "stable",
    Connection: "control", // the connection name of the relay's bootstrap file
    // ManifestKey defaults to manifests/stable.yaml; Prefix goes before every key.
})
if err != nil {
    return err
}

res, err := pub.Publish(ctx, s3publish.Request{
    Objects: []s3publish.Object{
        {Name: "checkout-flags", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: flagsYAML},
        {Name: "flagsets", Kind: controlplane.KindFlagSets, Format: controlplane.FormatYAML, Body: setsYAML},
    },
})
switch {
case errors.Is(err, s3publish.ErrConflict):
    // another publisher won; nothing visible changed. Read again and decide.
    return err
case errors.Is(err, s3publish.ErrInvalid):
    // nothing was written; the error wraps the controlplane error.
    return err
case err != nil:
    // a store call failed (*s3publish.OperationError); repeating is safe.
    return err
}

// res is nil whenever err is not nil: use it only here.
fmt.Println(res.Generation, res.Uploaded, res.Present, res.Written)
```

## API

| Name | Meaning |
|---|---|
| `New(api API, cfg *Config) (*Publisher, error)` | No S3 call. `Bucket`, `Channel` and `Connection` are required. |
| `(*Publisher).Publish(ctx, Request) (*Result, error)` | Validate, upload absent objects, write the manifest last. |
| `(*Publisher).Verify(ctx) (*Report, error)` | Read the manifest and every object of the connection; per-object problems in the report. |
| `Request.DryRun` | The same reads and no write. |
| `Request.Generation` | Zero: the generation in the bucket plus one. An explicit value must be higher. |
| `Request.ReplaceInvalidManifest` | Replace a manifest in the bucket that is unreadable, invalid or over the limits. Needs an explicit `Generation`; still `If-Match`. |
| `Config.Check` | `func(*Object) error`, run for every object before any call, and by `Verify` for every object it read: the hook for validation beyond `controlplane`, such as the engine (see below). |
| `Config.MaxObjectBytes`, `MaxTotalBytes`, `MaxManifestBytes`, `MaxObjects` | The limits of the reader; zero means its defaults (16 MiB, 1 GiB, 16 MiB, 20000). |
| `Config.Concurrency` | Object calls at a time; zero means 8. |
| `Object.Source` | Where a document came from (a path), used only in error messages. |

Errors (test with `errors.Is` and `errors.As`): `ErrInvalid` (wraps the `controlplane` error), `ErrConfig`, `ErrConflict`,
`ErrGeneration`, `ErrChannelMismatch`, `ErrPreviousManifest`, `ErrObjectMismatch`, `ErrNoManifest` and `*OperationError`
(operation, key and the client error). A missing bucket (`NoSuchBucket`, any 404 that is not `NoSuchKey` or `NotFound`) is an
`*OperationError`, never "no manifest" and never a conflict.

## Keys and the reader

A manifest key that is also the key of one of the objects (its key is decided by its content) is refused (`ErrManifestKey`): the
manifest would overwrite the object it lists.

The keys written in the manifest are absolute keys of the bucket: `Config.Prefix` is put before them by the publisher and the
reader must not add a prefix. `Config.Connection` must be the control-plane connection name of the relay.

## Limits

Before any call the publisher refuses what the reader rejects, with `ErrInvalid` naming the object (never its content):
an object of 0 bytes or over `MaxObjectBytes`, the distinct objects over `MaxTotalBytes`, a marshalled manifest over
`MaxManifestBytes`, more than `MaxObjects` entries. `Verify` applies the same limits. The defaults are exported by
`controlplane` (`DefaultMaxObjectBytes`, `DefaultMaxTotalBytes`, `DefaultMaxManifestBytes`, `DefaultMaxObjects`).

## Checking flag files with the engine

`s3publish` imports only `controlplane` and the AWS SDK, on purpose. To also run the engine of the relay on every flag file,
pass the `controlplane/flagcheck` package through the hook:

```go
cfg.Check = func(obj *s3publish.Object) error {
    if obj.Kind != controlplane.KindFlags {
        return nil
    }

    return flagcheck.Check(obj.Body, obj.Format) // the loader and IsValid the relay uses; names the flag
}
```

`flagcheck` imports the engine (heavy): only importers of `flagcheck` pull it. A refusal is `ErrInvalid` and writes nothing. `Verify`
runs the same hook, so a stored flag file that the relay would reject is `invalid-content` even when its size and checksum match.
A flag file that a manifest lists with `format: toml` is not read by the format package: `Verify` accepts it as far as the
structure goes and leaves it to the hook (`flagcheck` reads TOML).

A generation of `math.MaxUint64` in the bucket has no successor: a changed publication is refused with `ErrGeneration`.

## Protocol

1. Validate the request with `controlplane.ValidateManifest`, `UnmarshalFlags` and `ValidateFlags`, `UnmarshalFlagSet` and
   `ValidateFlagSet` (names, kinds, formats, source references, duplicate keys, object limit). No call is made when it fails.
   Flag-set documents are validated together (`controlplane.ValidateFlagSetDocuments`): duplicate flag-set names and API keys
   across documents, unresolved sources.
2. `GetObject` the manifest: its bytes and ETag. Invalid, over the limits or of another channel: an error, never an overwrite
   (unless `ReplaceInvalidManifest` with an explicit generation).
3. Objects sorted by name get `Prefix + controlplane.ObjectKey(checksum, format)`. Same documents as the manifest in the bucket
   and no higher explicit generation: no manifest is written, but step 4 still runs, so a missing listed object is uploaded again.
4. Per distinct key, at most `Concurrency` at a time: `HeadObject` (checksum mode on). Present with the expected size and the
   expected sha256: `StatusPresent`. When the store reports no sha256, or a composite one, the object is read and hashed; a
   mismatch is `ErrObjectMismatch`. Absent: `PutObject` with `If-None-Match: *` and `ChecksumSHA256`. A `412` there means another
   writer created it: the key is checked again and accepted if it matches. The first error cancels the rest.
5. `PutObject` the manifest, alone and last: `If-Match: <ETag>`, or `If-None-Match: *` when there was none. `412`,
   `409 ConditionalRequestConflict` and `404 NoSuchKey` (manifest deleted) trigger one `GetObject` of the manifest: if it is
   the manifest we tried to write (same channel, generation and entries), an earlier attempt of ours committed and the
   result is success with its ETag; otherwise `ErrConflict`.

The manifest is YAML, or JSON when the manifest key ends in `.json`.

## Guarantees

- A manifest in the bucket never lists an object that is not there.
- Of two publishers that read the same manifest, one write succeeds and the other gets `ErrConflict`.
- The generation strictly increases.
- A repeat after any error is safe and converges.
- Old objects are not deleted: garbage collection is a later change. Objects left by an interrupted publish are harmless.
- The IAM identity needs `s3:GetObject` (also for `HeadObject`) and `s3:PutObject`; `Verify` of a manifest that pins a
  `versionId` also needs `s3:GetObjectVersion`. Without `s3:ListBucket` S3 answers a missing key with 403 instead of 404
  (documented S3 behaviour): the publisher cannot tell it from a real denial, so it returns an `*OperationError` whose text
  names the cause, and never takes the object for present or the manifest for absent. `Verify` reports it as `unreadable`.

## Verify

`Verify` reads the manifest and each object of `Config.Connection` (the pinned `versionId` of an entry, when it has one; an absent
version is `missing`) and reports `Problem{Object, Key, Code, Detail}`:
`missing`, `unreadable`, `size-mismatch`, `checksum-mismatch`, `invalid-content`, `manifest-invalid`, or the `controlplane`
code of a validation problem such as `unresolved-source`. Objects of other connections are in `Skipped`. `Detail` never holds
document content, so no API key. `ErrNoManifest` and `*OperationError` are returned as errors.
