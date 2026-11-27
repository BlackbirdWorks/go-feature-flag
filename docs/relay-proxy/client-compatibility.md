# Client compatibility

This page tracks what the provider integration suites in `openfeature/provider_tests/` test against the version 2 relay proxy.

The current suites run unmodified `GO Feature Flag` OpenFeature providers against two relay proxy instances:

- `bootstrap.yaml` (`:1031`) with `evaluation.contextEnrichment` enabled
- `bootstrap-authenticated.yaml` (`:1032`) for API key authentication checks

Both instances set `evaluation.disableSemaphoreMetadata: true` so response metadata matches what the upstream providers assert. Data collector and tracking event coverage depends on `#9` and is omitted until that pipeline is ready.

## Provider coverage

| Language | Provider | Pinned version | Value types | Bulk evaluation | Evaluation context | Error codes | Data collector / tracking | Flag change polling | WebSocket |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Go | `github.com/open-feature/go-sdk-contrib/providers/go-feature-flag` (SDK `v1.20.0`, `EvaluationTypeRemote`) | `v1.1.3` (`go.mod`) | bool, string, float, int, object (match, disabled, not-found; type mismatch for bool/string/float/int) | Not covered | Targeting key, custom attributes (string, bool, float, int, list, nested object), semver targeting, server `contextEnrichment` | `FLAG_NOT_FOUND`, `TYPE_MISMATCH`, `GENERAL` (invalid/missing API key) | Not covered (depends on `#9`) | Not covered | Not covered |
| Java | `dev.openfeature.contrib.providers:go-feature-flag` (SDK `1.23.0`) | `2.0.2` (`pom.xml`) | bool, string, double, int, object (match, disabled, not-found; type mismatch for bool/string/double/int) | Not covered | Targeting key, custom attributes (string, bool, double, int, list, nested structure) | `FLAG_NOT_FOUND`, `TYPE_MISMATCH`, `PROVIDER_FATAL` (invalid/empty API key on init) | Not covered (depends on `#9`) | Not covered | Not covered |
| .NET | `OpenFeature.Contrib.GOFeatureFlag` (SDK `2.14.1`) | `0.2.1` (`ProviderTests.csproj`) | bool, string, double, int, object (match, disabled, not-found; type mismatch for bool/string/double/int; disabled object uses `disabled_int`) | Not covered | Targeting key, custom attributes (string, bool, double, int, list, nested structure) | `FLAG_NOT_FOUND`, `TYPE_MISMATCH`, `GENERAL` (invalid/empty API key) | Not covered (depends on `#9`) | Not covered | Not covered |
| JavaScript | `@openfeature/go-feature-flag-provider` (SDK `1.23.0`; peer `@openfeature/ofrep-provider` `0.2.4`) | `^2.2.0` (`package.json`), locked `2.2.0` (`package-lock.json`) | bool, string, number (float only), object (match, disabled, type mismatch, not-found; disabled object uses `disabled_int`) | Not covered | Targeting key, custom attributes (string, bool, number, array, nested object); global `gofeatureflag.flagList` set but unasserted | `FLAG_NOT_FOUND`, `TYPE_MISMATCH`, `PROVIDER_FATAL` (invalid/empty API key on init) | Not covered (depends on `#9`) | Not covered | Not covered |

## Known gaps

- No dedicated OFREP conformance suite or OFREP-only provider suites yet (Go, JavaScript server/web, Java, .NET, Python).
- Bulk evaluation, flag change polling, and WebSocket notifications are uncovered across all four suites.
- Object `TYPE_MISMATCH` is missing in Go, Java, and .NET; the .NET and JavaScript disabled-object tests evaluate `disabled_int` instead of `disabled_interface`; JavaScript number tests cover floats only (not integers).
- Server-side `evaluation.contextEnrichment` and semver targeting rules are only tested in Go.
- Error codes `PARSE_ERROR`, `TARGETING_KEY_MISSING`, `INVALID_CONTEXT`, and rate limiting (`429` / `Retry-After`) are not covered.
- JavaScript `package.json` uses a caret range (`^2.2.0`) rather than an exact pinned version (resolved to `2.2.0` in `package-lock.json`).
- Data collector and tracking events depend on `#9` and are intentionally left out.
