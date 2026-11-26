# Conformance vectors, schema v1

Documents for `apiVersion: semaphore/v1`. They are the conformance suite of the `controlplane` package and are meant
to be run by any other implementation (the relay reader, the publisher, an external control plane) as well.

File names are `<type>-<case>.<yaml|json>` where `<type>` is `manifest`, `flagset` or `flags`.

## valid/

Decode without error and without unknown fields, then pass validation. Flag-set vectors are validated against
`valid/manifest-basic.yaml` (its objects `checkout-flags` of kind `flags` and `flagsets` of kind `flagSets`).

- `manifest-basic.yaml`, `manifest-basic.json`: smallest manifest with a flag object and a flag-set object.
- `manifest-two-connections.yaml`: `versionId`, a JSON-format flag object, two flag objects on two connections.
- `flagset-basic.yaml`, `flagset-basic.json`: one flag set, one source, one API key.
- `flagset-multiple.yaml`: two flag sets, two keys, a flag set without keys.
- `flags-basic.yaml`, `flags-basic.json`: headerless flag file with a targeting rule and percentages.
- `flags-rollouts.yaml`: progressive, scheduled and experimentation rollouts, `trackEvents`, `version`, `metadata`.
- `flags-quoted-strings.yaml`: strings that need quotes (`yes`, `no`, `007`, `1e3`, `2026-01-01`, `null`).

## invalid/

Each file has a `<file>.error` sidecar: line 1 is the expected error class, line 2 a text the error message
contains. Classes: `ErrWrongKind`, `ErrUnsupportedAPIVersion`, `ErrInvalid` (validation, the message starts with the
path of the field), `ErrDecode` (syntax). The document is rejected whole.

- `manifest-wrong-kind.yaml`: a manifest whose kind is `FlagSet`.
- `manifest-unsupported-version.yaml`: `apiVersion: semaphore/v2`.
- `manifest-duplicate-name.yaml`: two objects with the same name.
- `manifest-bad-checksum.yaml`: a checksum that is not 64 hex characters.
- `manifest-missing-size.yaml`: size 0.
- `manifest-unknown-object-kind.yaml`: object kind other than `flags` or `flagSets`.
- `manifest-zero-generation.yaml`: generation below 1.
- `manifest-syntax-error.yaml`: not parseable.
- `flagset-wrong-kind.yaml`: a flag-set document whose kind is `Manifest`.
- `flagset-unresolved-source.yaml`: a source that the manifest does not list.
- `flagset-duplicate-key.yaml`: an API key used by two flag sets.
- `flagset-duplicate-name.yaml`: two flag sets with one name.
- `flags-unknown-variation.yaml`: a rule that names an undeclared variation.
- `flags-bad-percentage.yaml`: a percentage above 100.

## forward-compatible/

Documents with fields this version does not know (a later publisher added them). They decode and validate; each
file has a `<file>.unknown` sidecar listing the expected unknown-field paths, one per line, sorted as the decoder
reports them. An older reader must accept them and may only count them.
