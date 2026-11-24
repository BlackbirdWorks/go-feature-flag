# Upstream tracking

This project is an independent derivative of
[thomaspoignant/go-feature-flag](https://github.com/thomaspoignant/go-feature-flag) (MIT).
This file records which upstream commit this tree was last synced to and how this tree differs
from upstream. The `upstream-catchup` skill (`.claude/skills/upstream-catchup/SKILL.md`) reads
and updates it.

## Isolation

This project never publishes to, pushes to, comments on or automatically syncs from upstream. The
only contact is the `upstream-catchup` skill: a manual, read-only review of upstream commits by a
developer, with changes ported selectively by hand (never a wholesale merge of `upstream/main`).
Dependabot ignores the upstream modules (`.github/dependabot.yml`).

## Marker (machine-readable)

Keep these lines as plain `key: value`, one per line, no quoting
(read with `sed -n 's/^last_reviewed_commit: //p' UPSTREAM.md`).

```text
upstream_repo: https://github.com/thomaspoignant/go-feature-flag
upstream_branch: main
last_reviewed_commit: 4b01ee52ecebff4a7cd6760623fbcb3f5a14b234
last_reviewed_date: 2026-11-23
```

`last_reviewed_commit` is the upstream commit `perf(core): cache the decoded JsonLogic query instead of
re-parsing it on every evaluation (#6565)`, committed 2026-11-23T15:21:22Z. Initial value was
`git merge-base main upstream/main`.

## Carried ahead of upstream

Changes in this tree that were open upstream pull requests when taken. When upstream merges,
changes or closes one, reconcile it (see the skill, class ALREADY-HAVE) instead of applying it twice.

| Upstream PR | Change | Merge commit here | Status |
|---|---|---|---|
| [#6386](https://github.com/thomaspoignant/go-feature-flag/pull/6386) | perf: keep a nikunjy evaluator per query across garbage collections | `677369c767b3f0e0019774022bec3955af1225a3` | open upstream |
| [#6542](https://github.com/thomaspoignant/go-feature-flag/pull/6542) | relayproxy: one shared poller per bucket for S3 flag sets (`enableSharedPollerPerBucket`) | `7014e6a181bb55a73fcaae84fb34346080ed5707` | open upstream |
| [#6571](https://github.com/thomaspoignant/go-feature-flag/pull/6571) | perf: hash a rule's query once at load | `fee78898f42947f131e4c75c3cf41df29811a891` | open upstream |
| [#6578](https://github.com/thomaspoignant/go-feature-flag/pull/6578) | perf(core): skip the JSON round trip for JsonLogic contexts that are already JSON-shaped (`asJSONData` in `modules/core/flag/query_jsonlogic_cache.go`, its test file, one call in `rule.go`). Ported by hand from head `b43464fe36b2b5e2b47b495141c884b23c9c6379`; replaces the earlier local `json_normalize.go`. Local additions on top: none (upstream's code passes the local differential test `TestQueryContext_DataOnce_MatchesTheJSONRoundTrip`, kept in `query_context_data_internal_test.go`). When upstream merges it, drop this carry and re-sync those files from upstream main. | this PR | open upstream |

Local deviation on the carried #6578 test file: `TestGetCompiledQuery_OnlyEvaluationStores` runs serially (it asserts the size of the process-wide cache that the other, parallel, tests fill); re-apply when re-syncing from upstream.

Status checked 2026-11-23 with `gh pr view <n> -R thomaspoignant/go-feature-flag --json state,mergedAt`.
PR #6565 was merged upstream before the baseline and is part of it, not carried.

## Vendored

| Path | Source | Pinned commit | Status |
|---|---|---|---|
| `rules/` | [github.com/go-feature-flag/rules](https://github.com/go-feature-flag/rules) | `3baa4ca8fb5ee47aca90ff6d3227fd4b0be5523b` (pull request #2 "perf: hash index for large IN lists", base `e9704fa7a06c`) | PR #2 closed (not merged) as of 2026-11-23 |

Consumed through `replace github.com/nikunjy/rules => ./rules` in the root `go.mod`.
Re-sync: see "Re-syncing vendored rules/" in the skill. `rules/LICENSE` must stay.

## Intentional divergences

Do not port upstream changes in these areas; classify them N/A-BY-DESIGN.

Done in this tree:

- WASM/WASI build of the evaluation engine removed (`cmd/wasm`, TinyGo-specific code paths, wasm release workflows/scripts).
- Go toolchain pinned to the latest release (`go 1.27.4`) in all modules.
- `rules` is consumed through a local `replace` directive, not a released module version. `modules/core` is a plain directory of the root module here (upstream: a separate module).
- Docs website, bundled OpenFeature provider SDKs (`openfeature/providers`, including the python provider's WASM submodule), `examples/` and the upstream release/community tooling removed; plain Markdown docs live in `docs/`.
- gopherstack-based end-to-end tests (`testutils/gopherstack`, `*_e2e_test.go`, build tag `docker`).

- Renamed to Semaphore: Go module path `github.com/blackbirdworks/semaphore`, binaries, images and operator-visible names, with
  the GO Feature Flag names kept as aliases (see "Path and name mapping" and `docs/migration/rename-to-semaphore.md`).
- `github.com/thomaspoignant/koanf-http-provider` is copied into `internal/koanfhttpprovider` (MIT, see `NOTICE`): no module
  controlled by the upstream author is a dependency.
- `cmd/relayproxy/testdata/web-provider-playground` (a dev tool built on the upstream npm provider) removed.

Planned, not done yet:

- Lint rules ported from BlackbirdWorks/gopherstack (`.golangci.yml` currently differs from upstream only by the local `replace` and `rules/` entries).
- CI reworked.

## Path and name mapping

When reading an upstream commit, map it to this tree like this. Never apply an upstream hunk that reintroduces an upstream name
without its Semaphore counterpart.

| Upstream | This repository |
|---|---|
| `github.com/thomaspoignant/go-feature-flag` (root module) | `github.com/blackbirdworks/semaphore` |
| `github.com/thomaspoignant/go-feature-flag/modules/core` (separate module upstream) | `github.com/blackbirdworks/semaphore/modules/core` (package directory of the root module) |
| `github.com/thomaspoignant/koanf-http-provider` | `github.com/blackbirdworks/semaphore/internal/koanfhttpprovider` |
| `cmd/relayproxy/handler/goff/` | `cmd/relayproxy/handler/semaphore/` |
| `cmd/relayproxy/api/routes_goff.go` | `cmd/relayproxy/api/routes_semaphore.go` |
| `cmd/relayproxy/testdata/goff/` | `cmd/relayproxy/testdata/semaphore/` |
| `goff-proxy.yaml`, `/goff/`, `/etc/opt/goff/` | `semaphore-proxy.yaml` first, then the legacy name (code in `cmd/relayproxy/config/config_loader.go`) |
| `gofeatureflag` evaluation context key, `gofeatureflag_cacheable`, `X-GOFEATUREFLAG-VERSION`, `gofeatureflag_` metrics | `semaphore` / `semaphore_cacheable` / `X-SEMAPHORE-VERSION` / `semaphore_`, the old names stay as aliases |
| binaries `go-feature-flag`, `go-feature-flag-cli`, images `thomaspoignant/go-feature-flag*` | `semaphore`, `semaphore-cli`, `ghcr.io/blackbirdworks/semaphore*` |

Mechanical part: rewrite the module path (`sed` on the incoming patch, see the skill) and review every hunk that mentions one of the
names above by hand. Upstream web links (`https://github.com/thomaspoignant/go-feature-flag/...`) are references and stay as they
are. The upstream provider tests (`openfeature/provider_tests`) keep depending on the published upstream packages on purpose.

## Sync log

| Date | From | To | Summary |
|---|---|---|---|
| 2026-11-23 | (fork point) | `4b01ee52ecebff4a7cd6760623fbcb3f5a14b234` | Initial baseline: merge-base of `main` and upstream `main`. Three upstream PRs carried, `rules/` vendored. |
