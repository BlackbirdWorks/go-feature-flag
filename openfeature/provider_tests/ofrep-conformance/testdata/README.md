# Vendored OFREP OpenAPI Specification

- **File**: [`openapi.yaml`](openapi.yaml)
- **Upstream source**: `https://github.com/open-feature/protocol/blob/98c4e0de5583c0d79dd938b82975c80125335e92/service/openapi.yaml`
- **Version**: OpenFeature Remote Evaluation Protocol (OFREP) `0.4.0` (pinned commit `98c4e0de5583c0d79dd938b82975c80125335e92`)

## Intentional Validator Rule (`validateValueOneOf`)

The vendored upstream schema is kept byte-for-byte unmodified. In `evaluationSuccess.properties.value` (and `value` / branch structure across `booleanFlag`, `stringFlag`, `integerFlag`, `floatFlag`, `objectFlag`, and `codeDefaultFlag`):

1. `codeDefaultFlag` omits `value` in its properties without an explicit `not: { required: [value] }` restriction in OpenAPI 3.1.
2. `integerFlag` (`type: integer`) also satisfies `floatFlag` (`type: number`).

To match the OFREP specification intent (OpenFeature OFREP ADR-0006, where returning code default omits `"value"` rather than emitting `"value": null`), [`validateValueOneOf`](../validator.go) enforces:

- `codeDefaultFlag` matches **only** when the `"value"` property is absent (so `"value": null` fails validation).
- Typed flag branches match **only** when the `"value"` property is present.
- Whole JSON numbers matching both `integerFlag` and `floatFlag` are accepted as an unambiguous numeric match.
