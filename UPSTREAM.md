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
- `modules/core` and `rules` are consumed through local `replace` directives, not released module versions.
- Docs website, bundled OpenFeature provider SDKs (`openfeature/providers`, including the python provider's WASM submodule), `examples/` and the upstream release/community tooling removed; plain Markdown docs live in `docs/`.
- gopherstack-based end-to-end tests (`testutils/gopherstack`, `*_e2e_test.go`, build tag `docker`).

Planned, not done yet:

- Go module path rename to `github.com/blackbirdworks/go-feature-flag`.
- Lint rules ported from BlackbirdWorks/gopherstack (`.golangci.yml` currently differs from upstream only by the local `replace` and `rules/` entries).
- CI reworked.

## Sync log

| Date | From | To | Summary |
|---|---|---|---|
| 2026-11-23 | (fork point) | `4b01ee52ecebff4a7cd6760623fbcb3f5a14b234` | Initial baseline: merge-base of `main` and upstream `main`. Three upstream PRs carried, `rules/` vendored. |
