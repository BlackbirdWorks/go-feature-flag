# Document versions and compatibility

Publishers and relays upgrade at different times. This page states which combinations work.

## The version fields

| Field | Where | Current value | Meaning |
|---|---|---|---|
| `apiVersion` | manifest, flag-set document, segments document | `semaphore/v1` | The version of the document format. |
| `kind` | the same documents | `Manifest`, `FlagSet`, `Segments` | The type of the document. |
| `version` | an entry of `objects` in the manifest | `1` (absent or `0` means `1`) | The version of the content schema of that source object. |

A flag object has no header: its format (`yaml`, `json`, `toml`) and its content `version` are in the manifest entry.

## Rules

- Within `semaphore/v1` only additive, optional fields are added. A field is never removed, renamed or re-typed, and the
  meaning of a value never changes. Such a change gets a new `apiVersion`, with new types next to the old ones.
- Additive fields are written in any order. A reader ignores a field it does not know and the document is read and
  applied. The decoder reports the unknown paths (for example `objects[2].tier`) in `DecodeInfo.UnknownFields`.
- Upgrade relays before publishers write a new field that changes behaviour. An old relay ignores such a field and
  serves the old behaviour.
- A document with another `kind` or an `apiVersion` that the reader does not support is rejected whole. For a
  manifest the relay keeps the held one and logs the outcome `invalid`. The reason is `wrong-kind` for another `kind`
  (a flag-set document in the place of the manifest) and `unsupported-version` for an `apiVersion` that the reader does
  not support.
- An object whose `version` is higher than the reader supports is an entry-level validation problem with the code
  `unsupported-version` (`Kind: EntryObject`). The manifest still applies: the other objects update as usual, and only
  that entry degrades to `stale` (keeping its last-known-good content) or `missing` (when never loaded) with reason
  `unsupported-version`; the flag sets that depend on it become `degraded` with `source-degraded` while keeping their
  last-known-good definitions. Once the object returns to `version: 1` (or the relay is upgraded), it recovers.

## Compatibility matrix

| Publisher writes | Relay is older | Relay is current |
|---|---|---|
| a new optional field | works, the field is ignored | works |
| a new field that changes behaviour | works, the behaviour is the old one | works |
| a newer content `version` of one object | only that object degrades (`unsupported-version`) keeping last-known-good; other objects update | works |
| a newer `apiVersion` | the manifest is rejected whole, the relay keeps serving the held one | works |
| the current format | works | works |

## Vectors

`controlplane/testdata/v1/forward-compatible/` holds forward-compatible documents: extra fields that this version
does not know (each with a `<file>.unknown` sidecar listing expected unknown paths) or an object entry with a newer
content `version` (with a `<file>.degraded` sidecar listing `<kind> <name> <code> <path>`):

- `manifest-extra-fields.yaml` and `manifest-extra-fields.json`
- `manifest-object-unsupported-version.yaml`
- `flagset-extra-fields.yaml`
- `flags-extra-fields.yaml`
- `segments-extra-fields.yaml`

`controlplane/testdata/v1/invalid/` holds the documents that every reader must reject, with the expected error class in a
sidecar `<file>.error`. Any other implementation of the format can run both sets, see the
[package page](controlplane.md#conformance-vectors).

A test pins every exported field of the structs with its `json` and `yaml` tags, so a change of the wire format fails
the build.
