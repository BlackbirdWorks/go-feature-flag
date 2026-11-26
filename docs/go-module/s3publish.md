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
case errors.Is(err, s3publish.ErrInvalid):
    // nothing was written; the error wraps the controlplane error.
case err != nil:
    // a store call failed (*s3publish.OperationError); repeating is safe.
}

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
| `Object.Source` | Where a document came from (a path), used only in error messages. |

Errors (test with `errors.Is` and `errors.As`): `ErrInvalid` (wraps the `controlplane` error), `ErrConfig`, `ErrConflict`,
`ErrGeneration`, `ErrChannelMismatch`, `ErrPreviousManifest`, `ErrObjectMismatch`, `ErrNoManifest` and `*OperationError`
(operation, key and the client error).

## Protocol

1. Validate the request with `controlplane.ValidateManifest`, `UnmarshalFlags` and `ValidateFlags`, `UnmarshalFlagSet` and
   `ValidateFlagSet` (names, kinds, formats, source references, duplicate keys, object limit). No call is made when it fails.
2. `GetObject` the manifest: its bytes and ETag. Invalid, or of another channel: an error, never an overwrite.
3. Objects sorted by name get `Prefix + controlplane.ObjectKey(checksum, format)`. Same documents as the manifest in the bucket
   and no higher explicit generation: `Written` is false and nothing more is sent.
4. Per distinct key: `HeadObject` (with the checksum mode on). Present with the expected size (and sha256, when the store
   reports it): `StatusPresent`. Absent: `PutObject` with `If-None-Match: *` and `ChecksumSHA256`. A `412` there means another
   writer created it: the key is checked again and accepted if it matches.
5. `PutObject` the manifest: `If-Match: <ETag>`, or `If-None-Match: *` when there was none. `412`, `409 ConditionalRequestConflict`
   and `404` (manifest deleted) are `ErrConflict`.

The manifest is YAML, or JSON when the manifest key ends in `.json`.

## Guarantees

- A manifest in the bucket never lists an object that is not there.
- Of two publishers that read the same manifest, one write succeeds and the other gets `ErrConflict`.
- The generation strictly increases.
- A repeat after any error is safe and converges.
- Old objects are not deleted: garbage collection is a later change.

## Verify

`Verify` reads the manifest and each object of `Config.Connection` and reports `Problem{Object, Key, Code, Detail}`:
`missing`, `unreadable`, `size-mismatch`, `checksum-mismatch`, `invalid-content`, `manifest-invalid`, or the `controlplane`
code of a validation problem such as `unresolved-source`. Objects of other connections are in `Skipped`. `Detail` never holds
document content, so no API key. `ErrNoManifest` and `*OperationError` are returned as errors.
