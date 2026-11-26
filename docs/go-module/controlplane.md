# Control plane document format (`controlplane`)

The package `github.com/blackbirdworks/semaphore/controlplane` is the format of the documents a publisher writes to a
store (for example S3) and that the relay proxy reads: the manifest, the flag-set documents and the flag objects.

- Import one package, fill exported structs, call a `Marshal` function, write the bytes.
- The relay is built to decode and validate with the same package, so "valid for the publisher" is "accepted by the
  relay". (The relay does not use it yet; it is wired in by the manifest reader and the flag-set work.)
- The production files import only the standard library and `gopkg.in/yaml.v3`.
- It is the public, stability-guaranteed Go package of the module: its API follows semantic versioning.

## Documents

| Document | Kind | Header | Content |
| --- | --- | --- | --- |
| Manifest | `Manifest` | `apiVersion: semaphore/v1` | the objects of one channel at one generation |
| Flag-set document | `FlagSet` | `apiVersion: semaphore/v1` | flag sets: sources and API keys |
| Flag object | none | none | a flag file: flag name to flag (flag format v1) |

The manifest is written last. It points to the other objects by connection, key, checksum and size. The three examples
below belong together: the manifest lists the flag object and the flag-set document with their real checksum and
size.

### Manifest

<!-- controlplane-doc: manifest -->
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
    key: objects/sha256/fd/fd6823b78a365e492abfd50adb2f3b2ff1a2a912a3940a038a648e6e6a892993.yaml
    format: yaml
    checksum:
      algorithm: sha256
      value: fd6823b78a365e492abfd50adb2f3b2ff1a2a912a3940a038a648e6e6a892993
    size: 209
  - name: checkout-sets
    kind: flagSets
    connection: control
    key: objects/sha256/aa/aa45ea648f6a66ea94b95670ba6f20ff12450b411edd3b7f976aa3b08d604a90.yaml
    format: yaml
    version: 1
    checksum:
      algorithm: sha256
      value: aa45ea648f6a66ea94b95670ba6f20ff12450b411edd3b7f976aa3b08d604a90
    size: 269
```

| Field | Rule |
| --- | --- |
| `channel` | required |
| `generation` | an integer, at least 1, increases with every publication of a channel |
| `createdAt` | optional, informational |
| `objects` | at most `DefaultMaxObjects` (20000) entries; the limit is an option, see below |
| `objects[].name` | `^[a-z][a-z0-9-]{0,62}$`, unique; flag sets refer to objects by name |
| `objects[].kind` | `flags` or `flagSets` |
| `objects[].connection` | name of a connection of the relay bootstrap file (it fixes bucket and credentials); never a secret |
| `objects[].key` | key in the bucket of the connection |
| `objects[].format` | `yaml`, `json` (and `toml` for `flags`) |
| `objects[].version` | optional integer; 1 (the default) is the headerless flag format; a newer one is an entry problem |
| `objects[].checksum` | `sha256` and 64 lower-case hex characters |
| `objects[].size` | bytes, 0 or more |
| `objects[].versionId` | optional version id of the store |

### Flag-set document

<!-- controlplane-doc: flagset -->
```yaml
apiVersion: semaphore/v1
kind: FlagSet
flagSets:
  - name: checkout
    sources:
      - checkout-flags
    apiKeys:
      - "6f1f1b7e-6a0e-4d4c-9d8e-2f6f3f1a0b11"
      - "0b0f58a6-5e2c-45f0-a1b1-1d4a2f0c7e22"
  - name: no-keys-yet
    sources:
      - checkout-flags
```

- `name`: same pattern as an object name, unique.
- `sources`: at least one name of a manifest object of kind `flags`; a later source wins on a duplicate flag key.
- `apiKeys`: plain inline strings (UUIDs), unique across all flag sets. They are secrets: neither the validator nor a
  decoder prints them. Protect the bucket (read-only for the relay, write for the publisher) and encrypt it at rest.

### Flag object

A flag object is the flag file format the engine loads today, without a header (a header key could collide with a flag
name). `Flags` is a `map[string]Flag`; a flag has `variations`, `targeting` rules, `defaultRule`, `scheduledRollout`,
`experimentation`, `metadata`, `trackEvents`, `disable`, `version`, `converter` and `bucketingKey`.

<!-- controlplane-doc: flags -->
```yaml
new-checkout:
  variations:
    "off": false
    "on": true
  targeting:
    - name: beta
      query: beta eq true
      percentage:
        "off": 0.0
        "on": 100.0
  defaultRule:
    variation: "off"
```

The structs mirror the engine's types exactly, so what the engine tells apart stays apart:

- Text fields the engine reads as optional (`name`, `query`, `variation`, `version`, `bucketingKey`, `converter`) are
  `*string`: absent is nil, an explicit empty string is a pointer to `""`.
- Maps and lists are written when they are not nil: `percentage: {}` or `variations: {}` is set but empty, and is
  kept. Nil is left out.
- A test keeps the structs equivalent to the engine's loader (`dto.DTO`) and fails when the engine gains a field.
- A nested `scheduledRollout` inside a scheduled step is not part of the format.

## Writing the documents

```go
func publish() error {
	flags := controlplane.Flags{"new-cart": {
		Variations:  map[string]any{"enabled": true, "disabled": false},
		DefaultRule: &controlplane.Rule{Variation: new("disabled")},
		Targeting: []controlplane.Rule{
			{Name: new("pilot"), Query: new(`plan eq "pilot"`), Variation: new("enabled")},
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

	key, err := controlplane.ObjectKey(sum, "yaml")
	if err != nil {
		return err
	}

	set := &controlplane.FlagSetDocument{FlagSets: []controlplane.FlagSet{{
		Name: "cart", Sources: []string{"cart-flags"}, APIKeys: []string{"9b2c4f3a-7d1e-4e0b-8a55-3c6d2f1e9a07"},
	}}}

	setBody, err := controlplane.MarshalFlagSet(set)
	if err != nil {
		return err
	}

	setSum := controlplane.ChecksumOf(setBody)

	setKey, err := controlplane.ObjectKey(setSum, "yaml")
	if err != nil {
		return err
	}

	manifest := &controlplane.Manifest{
		Channel:    "prod",
		Generation: 1,
		Objects: []controlplane.ManifestObject{
			{
				Name:       "cart-flags",
				Kind:       controlplane.KindFlags,
				Connection: "cart-bucket",
				Key:        key,
				Checksum:   sum,
				Size:       int64(len(body)),
				Format:     controlplane.FormatYAML,
			},
			{
				Name:       "cart-sets",
				Kind:       controlplane.KindFlagSets,
				Connection: "cart-control",
				Key:        setKey,
				Checksum:   setSum,
				Size:       int64(len(setBody)),
				Format:     controlplane.FormatYAML,
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
	_ = manifestBody

	return nil
}
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
| `ObjectKey(sum, ext) (string, error)` | `objects/sha256/<2 hex>/<hex>.<ext>`; `ext` is `[a-z0-9]+`, the checksum a valid sha256 |
| `WithMaxObjects(n)` | validation option; the manifest object limit (default `DefaultMaxObjects`, 20000) |

- `ChecksumOf` is not called `Checksum` because `Checksum` is the wire struct.
- `UnmarshalFlags` reads `yaml` (or an empty format) and `json`; `toml` returns `ErrUnsupportedFormat`.

### Errors

- `*DecodeError` (matches `ErrDecode`): a document that is not readable. It has a path, a reason and a line, and never
  the text of a value (a value can be an API key).
- `*KindError` (matches `ErrWrongKind`) and `*UnsupportedVersionError` (matches `ErrUnsupportedAPIVersion`): the
  document is rejected whole. Both also match `ErrInvalid`, as does every validation failure.
- `*ValidationError` (matches `ErrInvalid`): a list of `FieldError`. Each has a stable `Code`, the `Kind` of the entry
  (`object`, `flagSet`, `flag`, `document`), its `Name` and `Index` (`NoIndex` when it has none), a `Path` and a
  `Reason`.
- `ErrChecksumMismatch`, `ErrInvalidChecksum`, `ErrUnsupportedChecksum`, `ErrInvalidExtension`,
  `ErrUnsupportedValue` (a Marshal function met NaN, infinity, a map with non-string keys, invalid UTF-8 or a type
  outside the format).

### Keeping the good entries

A reader that wants a last-known-good per entry uses the `FieldError` fields, not the text:

- Codes: `unresolved-source`, `duplicate-key`, `duplicate-name`, `invalid-name`, `unsupported-version`,
  `invalid-checksum`, `invalid-value`, `missing-value`, `too-many-objects`, `undeclared-variation`.
- An object that has a problem is one with a `FieldError` of kind `object` and its `Index`; the other objects are good.
  The same holds for flag sets (`flagSet`) and flags (`flag`, by `Name`).
- An object with a newer content `version` is an entry problem (`unsupported-version`), not a failure of the manifest.
- Of two objects or flag sets with one name, the first in document order keeps the name and the later one has the
  problem. Of an API key held by two flag sets, the first flag set keeps the key and the later one has the problem
  (`duplicate-key`); the key itself is never printed.
- `ValidateFlags` uses the structural rules of the engine's own validation, so an accepted flag is not dropped when
  the relay loads it: variations present, none null, all of one type (a number above the int64 range has no type in the engine); a default rule; enabled targeting rules with a
  query and an outcome; declared variations; finite, non-negative percentages with a non-zero sum; progressive
  rollouts with both steps, variations and dates in order; the same checks on scheduled steps (whose rules may update
  a named rule partially). A disabled targeting rule is not checked. Queries are compiled by the engine, not here.

## Wire rules

- Output is deterministic: fields in the order of the structs, map keys sorted, in YAML and in JSON alike. The same
  input gives the same bytes, so checksums and publishing are reproducible.
- YAML and JSON describe the same document and are read by one reader. Field names are case sensitive. Duplicate keys,
  non-string keys, NaN and infinity, a second YAML document and data after a JSON value are errors.
- Numbers keep their type. A float is written with a fraction (`2.0`) and read back as a float; an integer stays an
  integer and exact, up to the `uint64` range, in both formats. An integer field rejects a fraction and an overflow.
- Strings are never changed. Every string that a YAML 1.1 or 1.2 reader could read as another type (`yes`, `no`, `on`,
  `off`, `y`, `n`, `true`, `null`, `007`, `1e3`, `2026-01-01`) and every string with a newline, tab or other control
  character or with leading or trailing whitespace is double quoted, in values, names and keys.
- Anchors, aliases and merge keys are accepted on read and never written.
- Strings must be valid UTF-8. A Marshal function returns `ErrUnsupportedValue` instead of dropping data: a struct field
  without a `yaml` name, NaN or infinity, a map with non-string keys. A `json.Number` is written as a number.
- A YAML merge key can bring in `apiVersion` and `kind`. A string with an anchor is not read as a timestamp field.

## Checksum and object key

- The checksum is the sha256 of the exact stored bytes, as 64 lower-case hex characters. Compute it over the bytes you
  upload, after marshalling. `VerifyChecksum` reports upper-case or malformed hex as `ErrInvalidChecksum`, not as a
  mismatch.
- The key is `objects/sha256/<first two hex>/<hex>.<ext>`: the content decides the key.
- A reader downloads an object, runs `VerifyChecksum` and compares the size; a mismatch rejects the object.

## Versions and forward compatibility

- Manifest and flag-set documents start with `apiVersion: semaphore/v1` and a `kind`. Flag objects have no header; their
  version and format are in the manifest entry.
- A document of another `kind` or an unsupported `apiVersion` is rejected whole. Handing a flag-set document to
  `UnmarshalManifest` fails on the first decode.
- Within a version only additive optional fields are allowed. Removing, renaming or re-typing a field, or changing the
  meaning of a value, is a breaking change and gets a new version (`semaphore/v2`) with new types side by side.
- Readers ignore unknown fields: `Unmarshal...` returns them in `DecodeInfo.UnknownFields` (for example
  `objects[2].tier`) and decoding succeeds. An older relay therefore reads the documents of a newer publisher.
- Upgrade relays before publishers write new fields that change behaviour. Additive fields are safe in any order.
- A test pins every exported field and its `json` and `yaml` tags, so a change of the wire format fails the build.

## Conformance vectors

`controlplane/testdata/v1/` holds documents any implementation can run against (`valid/`, `invalid/` with the expected
error, `forward-compatible/` with the expected unknown fields). See its `README.md`. A test checks that the examples on
this page are exactly what the code produces and that they validate.
