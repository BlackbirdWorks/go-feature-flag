# Control plane document format (`controlplane`)

The package `github.com/blackbirdworks/semaphore/controlplane` is the format of the documents a publisher writes to a
store (for example S3) and that the relay proxy reads: the manifest, the flag-set documents and the flag objects.

- Import one package, fill exported structs, call a `Marshal` function, write the bytes.
- The relay decodes and validates with the same package, so "valid for the publisher" is "accepted by the relay".
- The production files import only the standard library and `gopkg.in/yaml.v3`.
- It is the public, stability-guaranteed Go package of the module: its API follows semantic versioning.

## Documents

| Document | Kind | Header | Content |
| --- | --- | --- | --- |
| Manifest | `Manifest` | `apiVersion: semaphore/v1` | the objects of one channel at one generation |
| Flag-set document | `FlagSet` | `apiVersion: semaphore/v1` | flag sets: sources and API keys |
| Flag object | none | none | a flag file: flag name to flag (flag format v1) |

The manifest is written last. It points to the other objects by connection, key, checksum and size.

### Manifest

```yaml
apiVersion: semaphore/v1
kind: Manifest
channel: stable
generation: 7
createdAt: 2026-01-02T03:04:05Z
objects:
  - name: checkout-flags
    kind: flags
    connection: flags-eu
    key: objects/sha256/75/75cc434ecfa663c2d1385c5712239ca2f931413c1123fd4eb275a5c6ff3450f6.yaml
    format: yaml
    checksum:
      algorithm: sha256
      value: 75cc434ecfa663c2d1385c5712239ca2f931413c1123fd4eb275a5c6ff3450f6
    size: 100
    versionId: 3HL4kqtJlcpXroDTDmJ+rmSpXd3dIbrHY
  - name: flagsets
    kind: flagSets
    connection: control
    key: objects/sha256/30/3066acde9a28a34448d0bd92129db7846192078200c62f27cb14ff5bbd473528.yaml
    format: yaml
    version: 1
    checksum:
      algorithm: sha256
      value: 3066acde9a28a34448d0bd92129db7846192078200c62f27cb14ff5bbd473528
    size: 100
```

| Field | Rule |
| --- | --- |
| `channel` | required |
| `generation` | at least 1, increases with every publication of a channel |
| `createdAt` | optional, informational |
| `objects` | at most `DefaultMaxObjects` (1000) entries; the limit is an option, see below |
| `objects[].name` | `^[a-z][a-z0-9-]{0,62}$`, unique; flag sets refer to objects by name |
| `objects[].kind` | `flags` or `flagSets` |
| `objects[].connection` | name of a connection of the relay bootstrap file (it fixes bucket and credentials); never a secret |
| `objects[].key` | key in the bucket of the connection |
| `objects[].format` | `yaml`, `json` (and `toml` for `flags`) |
| `objects[].version` | optional, 1 (the default) is the headerless flag format |
| `objects[].checksum` | `sha256` and 64 lower-case hex characters |
| `objects[].size` | bytes, at least 1 |
| `objects[].versionId` | optional version id of the store |

### Flag-set document

```yaml
apiVersion: semaphore/v1
kind: FlagSet
flagSets:
  - name: checkout
    sources:
      - checkout-flags
    apiKeys:
      - 6f1f1b7e-6a0e-4d4c-9d8e-2f6f3f1a0b11
      - 0b0f58a6-5e2c-45f0-a1b1-1d4a2f0c7e22
  - name: no-keys-yet
    sources:
      - checkout-flags
```

- `name`: same pattern as an object name, unique.
- `sources`: at least one name of a manifest object of kind `flags`; a later source wins on a duplicate flag key.
- `apiKeys`: plain inline strings (UUIDs), unique across all flag sets. They are secrets: the validator never prints
  them. Protect the bucket (read-only for the relay, write for the publisher) and encrypt it at rest.

### Flag object

A flag object is the flag file format the engine loads today, without a header (a header key could collide with a flag
name). `Flags` is a `map[string]Flag`; a flag has `variations`, `targeting` rules, `defaultRule`, `scheduledRollout`,
`experimentation`, `metadata`, `trackEvents`, `disable`, `version`, `converter` and `bucketingKey`.

```yaml
new-checkout:
  variations:
    "off": false
    "on": true
  targeting:
    - name: beta
      query: beta eq true
      percentage:
        "off": 0
        "on": 100
  defaultRule:
    variation: "off"
```

The structs are light copies of the engine's types. A test keeps them equivalent to the engine's loader (`dto.DTO`).
A nested `scheduledRollout` inside a scheduled step is not part of the format.

## Writing the documents

```go
flags := controlplane.Flags{"new-cart": {
	Variations:  map[string]any{"enabled": true, "disabled": false},
	DefaultRule: &controlplane.Rule{Variation: "disabled"},
	Targeting: []controlplane.Rule{
		{Name: "pilot", Query: `plan eq "pilot"`, Variation: "enabled"},
	},
}}

err := controlplane.ValidateFlags(flags)
if err != nil {
	return err
}

body, err := controlplane.MarshalFlags(flags)
if err != nil {
	return err
}

sum := controlplane.ChecksumOf(body)

set := &controlplane.FlagSetDocument{FlagSets: []controlplane.FlagSet{{
	Name: "cart", Sources: []string{"cart-flags"}, APIKeys: []string{"6f1f1b7e-6a0e-4d4c-9d8e-2f6f3f1a0b11"},
}}}

setBody, err := controlplane.MarshalFlagSet(set)
if err != nil {
	return err
}

setSum := controlplane.ChecksumOf(setBody)

manifest := &controlplane.Manifest{
	Channel:    "prod",
	Generation: 1,
	Objects: []controlplane.ManifestObject{
		{
			Name: "cart-flags", Kind: controlplane.KindFlags, Connection: "flags-eu",
			Key: controlplane.ObjectKey(sum, "yaml"), Checksum: sum, Size: int64(len(body)), Format: controlplane.FormatYAML,
		},
		{
			Name: "cart-sets", Kind: controlplane.KindFlagSets, Connection: "control",
			Key: controlplane.ObjectKey(setSum, "yaml"), Checksum: setSum, Size: int64(len(setBody)), Format: controlplane.FormatYAML,
		},
	},
}

// The same checks the relay runs. An empty header means the current one.
err = controlplane.ValidateManifest(manifest)
if err != nil {
	return err
}

err = controlplane.ValidateFlagSet(set, manifest)
if err != nil {
	return err
}

manifestBody, err := controlplane.MarshalManifest(manifest)
if err != nil {
	return err
}

// Upload body and setBody first, manifestBody last.
```

Upload the objects first and the manifest last, so a manifest never points at content that is not yet readable. The
object keys are content addressed and immutable, so two writers never overwrite an object with other content.

## API

| Function | Purpose |
| --- | --- |
| `MarshalManifest`, `MarshalFlagSet`, `MarshalFlags` | deterministic YAML (the default written format) |
| `MarshalManifestJSON`, `MarshalFlagSetJSON`, `MarshalFlagsJSON` | the same documents as indented JSON |
| `UnmarshalManifest`, `UnmarshalFlagSet`, `UnmarshalFlags(data, format)` | read YAML or JSON; return `DecodeInfo` |
| `ValidateManifest(m, opts...)`, `ValidateFlagSet(d, m)`, `ValidateFlags(f)` | the checks the relay runs |
| `ChecksumOf(data)`, `VerifyChecksum(data, sum)` | sha256 of the exact bytes |
| `ObjectKey(sum, ext)` | `objects/sha256/<2 hex>/<hex>.<ext>` |
| `WithMaxObjects(n)` | validation option; the manifest object limit (default `DefaultMaxObjects`, 1000) |

- `ChecksumOf` is not called `Checksum` because `Checksum` is the wire struct.
- `ValidateFlags` checks structure only (variations declared, percentages in 0..100, complete progressive rollouts).
  It does not compile queries; the engine does that when it loads the flags.
- `UnmarshalFlags` reads `yaml` (or an empty format) and `json`; `toml` returns `ErrUnsupportedFormat`.
- Errors: `ErrUnsupportedAPIVersion`, `ErrWrongKind`, `ErrDecode`, `ErrChecksumMismatch`; validation returns a
  `*ValidationError` (matches `ErrInvalid`) with one path-addressed problem per field, such as
  `objects[1].checksum.value`.

### Deterministic output

- Fields keep the order of the structs and map keys are sorted.
- No anchors or aliases are written.
- Every string that a YAML 1.1 or 1.2 reader could read as another type is double quoted: `yes`, `no`, `on`, `off`, `y`,
  `n`, `true`, `null`, numbers (`007`, `1e3`), timestamps (`2026-01-01`) and similar. Names and keys too.
- The same input gives the same bytes, so checksums and publishing are reproducible.

## Checksum and object key

- The checksum is the sha256 of the exact stored bytes, as 64 lower-case hex characters. Compute it over the bytes you
  upload, after marshalling.
- The key is `objects/sha256/<first two hex>/<hex>.<ext>`: the content decides the key.
- A reader downloads an object, runs `VerifyChecksum` and compares the size; a mismatch rejects the object.

## Versions and forward compatibility

- Manifest and flag-set documents start with `apiVersion: semaphore/v1` and a `kind`. Flag objects have no header; their
  version and format are in the manifest entry.
- A document of another `kind` or an unsupported `apiVersion` is rejected whole (`ErrWrongKind`,
  `ErrUnsupportedAPIVersion`). Handing a flag-set document to `UnmarshalManifest` fails on the first decode.
- Within a version only additive optional fields are allowed. Removing, renaming or re-typing a field, or changing the
  meaning of a value, is a breaking change and gets a new version (`semaphore/v2`) with new types side by side.
- Readers ignore unknown fields: `Unmarshal...` returns them in `DecodeInfo.UnknownFields` (for example
  `objects[2].tier`) and decoding succeeds. An older relay therefore reads the documents of a newer publisher.
- Upgrade relays before publishers write new fields that change behaviour. Additive fields are safe in any order.
- A test pins every exported field and its `json` and `yaml` tags, so a change of the wire format fails the build.

## Conformance vectors

`controlplane/testdata/v1/` holds documents any implementation can run against (`valid/`, `invalid/` with the expected
error, `forward-compatible/` with the expected unknown fields). See its `README.md`.
