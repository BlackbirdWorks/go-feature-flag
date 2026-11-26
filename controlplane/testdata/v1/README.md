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
- `flags-control-strings.yaml`: strings with newlines, tabs and edge spaces, in values and in metadata.
- `flags-numbers.yaml`: `2` and `2.0` are an integer and a float and stay so; integers above 2^53 and above 2^63 are exact.
- `flags-empty-values.yaml`: explicit empty strings, an empty variation name and an empty map are values, not absence.
- `flags-disabled-rule.yaml`: a disabled targeting rule needs no query or outcome.
- `flags-flow-style.yaml`, `flags-anchors.yaml`: YAML flow style, anchors and merge keys are read.
- `manifest-merged-header.yaml`: `apiVersion` and `kind` can come from a YAML merge key.

## invalid/

Each file has a `<file>.error` sidecar: line 1 is the expected error class, line 2 a text the error message
contains (a path, or a reason). Classes: `ErrWrongKind`, `ErrUnsupportedAPIVersion`, `ErrInvalid` (validation, the message starts with the
path of the field), `ErrDecode` (syntax). The document is rejected whole.

- `manifest-wrong-kind.yaml`: a manifest whose kind is `FlagSet`.
- `manifest-unsupported-version.yaml`: `apiVersion: semaphore/v2`.
- `manifest-duplicate-name.yaml`: two objects with the same name.
- `manifest-bad-checksum.yaml`, `manifest-upper-case-checksum.yaml`: a checksum that is not 64 lower-case hex characters.
- `manifest-negative-size.yaml`: a negative size.
- `manifest-size-zero.yaml`: a size of 0 (the reader rejects an empty object).
- `manifest-object-unsupported-version.yaml`: an object with a newer content version (an entry problem).
- `manifest-generation-fraction.yaml`, `manifest-generation-overflow.yaml`: an integer field with a fraction or an overflow.
- `manifest-multiple-documents.yaml`: a second YAML document.
- `manifest-duplicate-key.yaml`, `manifest-json-duplicate-key.json`, `manifest-non-string-key.yaml`: duplicate and non-string keys.
- `flagset-api-keys-not-a-list.yaml`: `apiKeys` as a scalar (the error must not repeat the value).
- `flags-nan-variation.yaml`: NaN.
- `flags-zero-percentages.yaml`, `flags-mixed-variation-types.yaml`, `flags-null-variation.yaml`, `flags-progressive-without-dates.yaml`, `flags-missing-default-rule.yaml`: flags that the engine would drop.
- `flags-huge-integer-variation.yaml`: a variation above the int64 range (the engine does not know its type).
- `manifest-recursive-merge.yaml`: a merge key that refers to itself.
- `manifest-unknown-object-kind.yaml`: object kind other than `flags` or `flagSets`.
- `manifest-zero-generation.yaml`: generation below 1.
- `manifest-syntax-error.yaml`: not parseable.
- `flagset-wrong-kind.yaml`: a flag-set document whose kind is `Manifest`.
- `flagset-unresolved-source.yaml`: a source that the manifest does not list.
- `flagset-duplicate-key.yaml`: an API key used by two flag sets.
- `flagset-duplicate-name.yaml`: two flag sets with one name.
- `flags-unknown-variation.yaml`: a rule that names an undeclared variation.
- `flags-bad-percentage.yaml`: a negative percentage.

## forward-compatible/

Documents with fields this version does not know (a later publisher added them). They decode and validate; each
file has a `<file>.unknown` sidecar listing the expected unknown-field paths, one per line, sorted as the decoder
reports them. An older reader must accept them and may only count them.

- `manifest-extra-fields.yaml`, `manifest-extra-fields.json`: unknown fields at the top level and in an object.
- `flagset-extra-fields.yaml`: unknown fields in a flag set and at the top level.
- `flags-extra-fields.yaml`: unknown fields in a flag and in its default rule.
