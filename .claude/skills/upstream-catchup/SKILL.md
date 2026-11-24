---
name: upstream-catchup
description: Catch up this derivative of thomaspoignant/go-feature-flag with upstream. Use when asked to "catch up from upstream", "what are we missing from upstream", "sync with upstream go-feature-flag", or to review upstream commits since the last sync.
---

# Catch up from upstream

`UPSTREAM.md` at the repo root is the only state needed: upstream URL, `last_reviewed_commit`,
the "Carried ahead of upstream" table, "Vendored", "Intentional divergences" and the "Sync log".
Everything below works with `git` and `gh` and does not require a pre-existing `upstream` remote.

## Rules of contact with upstream (read first)

This skill is the only sanctioned contact with upstream (`thomaspoignant/go-feature-flag` and
`go-feature-flag/rules`), and that contact is manual, read-only and run by a developer.

- Read-only towards upstream: only `git fetch`, `git log`, `git diff`, `git show`, `gh api` GET calls
  (e.g. `compare`) and `gh pr view`. Never push, tag, open or comment on issues or pull requests,
  review, star, fork or otherwise write to any upstream repository, registry or organisation.
- Never `git merge upstream/main` (or rebase onto it) wholesale. Upstream changes are ported
  selectively, by hand, one reviewed commit at a time (step 5).
- Never run it on a schedule or from CI; it is not automation.
- Every write goes to this repository only: pass `-R BlackbirdWorks/semaphore` to `gh pr create`
  and similar commands, and keep the `upstream` remote push-disabled (step 1).

Report-only mode ("what are we missing?"): do steps 1-3, print the triage table, change no files.

## 1. Read the marker and fetch upstream

```bash
URL=$(sed -n 's/^upstream_repo: //p' UPSTREAM.md)
BASE=$(sed -n 's/^last_reviewed_commit: //p' UPSTREAM.md)
REPO=${URL#https://github.com/}          # thomaspoignant/go-feature-flag
git remote get-url upstream >/dev/null 2>&1 || git remote add upstream "$URL"
git remote set-url --push upstream DISABLED   # fetch-only: a push to upstream must be impossible
git fetch upstream main --tags
git rev-parse upstream/main              # target; equal to $BASE means nothing to do
```

## 2. Inventory

Git form (authoritative, no limits):

```bash
git log --reverse --format='%h%x09%cs%x09%s' "$BASE"..upstream/main
git diff --name-status "$BASE" upstream/main
git diff --stat "$BASE" upstream/main | tail -1
git show <sha>                           # per-commit detail
```

GitHub compare form (no fetch needed, also gives PR-merge timestamps):

```bash
gh api "repos/$REPO/compare/$BASE...main" --jq '{total: .total_commits, status: .status, files: (.files|length)}'
gh api "repos/$REPO/compare/$BASE...main" --jq '.commits[] | "\(.sha[0:10])\t\(.commit.author.date)\t\(.commit.message | split("\n")[0])"'
gh api "repos/$REPO/compare/$BASE...main" --jq '.files[] | "\(.status)\t\(.filename)"'
gh api "repos/$REPO/compare/$BASE...main" --jq '.files[] | select(.filename=="go.mod") | .patch'
```

The compare API returns at most 250 commits and 300 files per call (`.total_commits` shows the true
count; `--paginate` with `?per_page=100` pages the files/commits). If `.total_commits` exceeds the
returned commits or files, use the git form. Subjects end in `(#NNNN)`: that is the upstream PR number.

## 3. Triage

Assign every upstream commit exactly one class. List security fixes and evaluation-correctness bugs
at the top of the table, whatever their class.

| Class | Rule |
|---|---|
| APPLIES | Evaluation engine (`modules/core`, root package), relay proxy, retrievers, exporters, notifiers, config, `internal/`, security fixes, dependency bumps of modules we still use. |
| N/A-BY-DESIGN | Matches an entry in "Intentional divergences" of `UPSTREAM.md`: wasm/tinygo, website/docs, OpenFeature provider sources, release tooling (release-please, goreleaser, release workflows), or any directory removed locally (check `git ls-files <path>`). State which entry. |
| ALREADY-HAVE | Equals or supersedes a row of "Carried ahead of upstream" (match by PR number in the subject, or by touched files, e.g. `modules/core`, `cmd/relayproxy/service`). Reconcile: compare upstream's merged version with ours (`git diff <our-merge-commit>^1 <upstream-sha> -- <files>`); prefer upstream's final form unless our tree has later changes on top; then move the row out of the carried table (note it in the Sync log). If upstream closed the PR unmerged, keep the row and mark status `closed upstream`. |
| NEEDS-DECISION | Behaviour, API or config changes; new features touching divergent areas (module path, removed dirs, CI); anything conflicting with local perf work or vendored `rules/`. Ask the owner; do not apply silently. |

Refresh carried/vendored statuses:

```bash
gh pr view <n> -R "$REPO" --json state,mergedAt        # each row of the carried table
gh pr view 2 -R go-feature-flag/rules --json state     # vendored rules
```

## 4. Working audit (before changing code)

Write `docs/upstream-sync-audit.md` with: metadata (base SHA, target SHA, date), a commit manifest
table (`sha | date | PR | subject | class | rationale | local target`), the file map for APPLIES
commits, and a verification matrix (command, result). This file is temporary: delete it before the
final commit and check before pushing:

```bash
! test -e docs/upstream-sync-audit.md \
  && ! git diff --name-only origin/main...HEAD | grep -qx docs/upstream-sync-audit.md \
  && echo ok || echo "audit file still present or committed"
```

It must never reach `main`.

## 5. Apply

Work on a branch (`chore/upstream-sync-<short-sha>`), never on `main`. Process APPLIES commits in
upstream order, one local commit per upstream commit or tight group; messages reference the upstream
SHA and PR number.

- Preferred: `git cherry-pick -x <sha>` (the `-x` line records the upstream SHA).
- Module path: detect the current one with `MOD=$(go list -m)` (root `go.mod`). If it is not
  `github.com/thomaspoignant/go-feature-flag`, rewrite incoming patches. Either
  `git cherry-pick -n <sha>`, then
  `git diff --name-only --cached | grep -E '\.(go|mod)$' | xargs sed -i "s#github.com/thomaspoignant/go-feature-flag#$MOD#g"`
  and `git commit -c <sha>`; or
  `git format-patch -1 --stdout <sha> | sed "s#github.com/thomaspoignant/go-feature-flag#$MOD#g" | git am -3 --trailer "(cherry picked from upstream $sha)"`.
  Do not rewrite `go.sum` hunks. Upstream's go.mod/go.sum hunks under `modules/core` have no counterpart here (`modules/core` is a plain directory of the root module): drop them and carry their dependency changes into the root `go.mod`.
  Upstream names that were renamed here (directories, binaries, `goff-proxy`, `gofeatureflag*` keys) are listed in
  `UPSTREAM.md`, "Path and name mapping": port such hunks by hand to the Semaphore name and keep the alias behaviour.
- Paths deleted locally (e.g. `cmd/wasm`): in a conflict, `git rm` the "deleted by us" files and
  continue; the rest of the commit still applies.
- Dependency bumps: do not cherry-pick `go.sum` hunks. Re-run the bump in each module that uses it
  (root only; dependencies of `modules/core` and `rules` live in the root `go.mod`): `go get <mod>@<ver> && go mod tidy`, then `make tidy-check`.
- ALREADY-HAVE: reconcile as described in step 3.

## 6. Verify

Real targets from `Makefile` (see also `AGENTS.md`); minimum set:

```bash
make tidy-check                 # go.mod/go.sum tidy in all modules
make build                      # all modules and binaries
make test                       # go test -race -tags=docker in every module; the docker tag needs Docker
make lint                       # golangci-lint over all modules
make vuln-check                 # when dependencies changed
```

Convention for any test you add: parallel (`t.Parallel()`) table-driven tests; no `export_test.go`.
`make provider-tests` and `make bench` only if the sync touched providers or evaluation hot paths.

## 7. Advance the marker and open a PR

1. In `UPSTREAM.md` set `last_reviewed_commit` to the upstream SHA reviewed (full 40 chars, normally
   `upstream/main` at fetch time), `last_reviewed_date`, update the carried/vendored tables, and append a
   Sync log row: date, from, to, counts per class, NEEDS-DECISION items still open.
2. `rm docs/upstream-sync-audit.md` and run the leak check from step 4.
3. Push the branch and open a PR (direct pushes to `main` are blocked) with the triage table in the
   description: `gh pr create -R BlackbirdWorks/semaphore --title "chore: sync with upstream <short-sha>" --body-file <table>`.

## Re-syncing vendored `rules/`

`rules/` is a copy of github.com/go-feature-flag/rules at the commit pinned in `UPSTREAM.md`
(see `rules/README.md`).

```bash
gh pr view 2 -R go-feature-flag/rules --json state,mergedAt,mergeCommit
gh api repos/go-feature-flag/rules/compare/<pinned>...main --jq '.commits[] | "\(.sha[0:10]) \(.commit.message | split("\n")[0])"'
git clone https://github.com/go-feature-flag/rules "$TMPDIR/rules-src" && git -C "$TMPDIR/rules-src" checkout <new-sha>
rsync -a --delete --exclude LICENSE --exclude go.mod --exclude go.sum --exclude .git "$TMPDIR/rules-src/" rules/
# then rewrite the upstream module path in the imports under rules/ to github.com/blackbirdworks/semaphore/rules
```

Keep `rules/LICENSE`, never re-add `rules/go.mod` or `rules/go.sum` (rules is part of the root module; merge any new dependency into the root `go.mod`), keep the rewritten import paths, re-add local-only
changes (`git diff` shows them), update the pinned commit in `rules/README.md` and `UPSTREAM.md`,
then run `make tidy-check lint test`.
If PR #2 was merged, pin to the merge commit; if closed, keep the pin and say so in the table.
