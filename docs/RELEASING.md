# Releasing Semaphore

A release is cut from the GitHub UI: run the `Release` workflow (`.github/workflows/release.yml`) on `main`
and choose the bump (`minor` or `major`). The workflow finds the latest stable tag, computes
the next version, creates and pushes the annotated tag, then builds and publishes that tag in the same run.
Nobody types a version or a tag by hand. It uses the repository `GITHUB_TOKEN` only: no other secret,
no other registry, nothing is pushed to a branch or to another repository.

Nothing has been released yet; everything below applies once the first release is published.

## What a release publishes

| Artifact | Name | Destination |
|---|---|---|
| Relay proxy archives | `semaphore_<version>_<Os>_<arch>.tar.gz` (`.zip` on Windows) for linux, darwin, windows on `x86_64` and `arm64` (64-bit only: there are no 32-bit builds) | GitHub Release of this repository |
| FIPS 140-3 relay proxy archives | `semaphore-fips_<version>_Linux_<x86_64\|arm64>.tar.gz` (binary `semaphore-fips`, built with `GOFIPS140` from `.fips-version`) | GitHub Release |
| CLI archives | `semaphore-cli_<version>_<Os>_<arch>.tar.gz` | GitHub Release |
| AWS Lambda bundle | `semaphore-aws-lambda_<version>.zip` (linux arm64, binary `bootstrap`) | GitHub Release |
| Checksums | `checksums.txt` | GitHub Release |
| SBOMs | one SPDX JSON per archive (`*.sbom.json`, made with syft) | GitHub Release |
| Build provenance | GitHub attestation for every file listed in `checksums.txt` | GitHub attestation store of this repository |
| Relay proxy images | `ghcr.io/blackbirdworks/semaphore:v<version>` and, for stable releases only, `:v<major>.<minor>`, `:v<major>`, `:latest` (distroless, linux/amd64, arm64); suffix `-bookworm`, `-trixie` (Debian slim) and `-fips` (amd64, arm64) for the variants | GitHub Container Registry |
| CLI image | `ghcr.io/blackbirdworks/semaphore-cli:v<version>` (+ `v<major>.<minor>`, `v<major>`, `latest` for stable releases only) | GitHub Container Registry |
| Documentation site | Built from `docs/` with VitePress (stable releases only, deployed in parallel with `release`) | GitHub Pages (`https://semaphore.blackbirdworks.dev/`) |

All images run as uid `65532` and use `/usr/local/bin/semaphore` as `ENTRYPOINT` (the CLI image uses `/semaphore-cli`). Pass arguments directly (`docker run IMAGE --config /path`), or override with `--entrypoint`. Images carry `org.opencontainers.image.*` labels (`source` = this repository, `license`, `version`, `revision`). Images are not attested or signed; release archives are (see below).

Not published (no destination owned by the project):

- Package managers and registries: Homebrew tap, Scoop bucket, Docker Hub, AWS public ECR, npm, PyPI, Maven, NuGet, WebAssembly packages.
- Helm chart (not carried over from the original project).
- GPG signatures (the project has no signing key; use GitHub attestations instead).

## Cut a release

1. Make sure `main` is green.
2. In GitHub **Actions** -> **Release** -> **Run workflow** (with **Use workflow from** set to `main`), leave `dry_run` checked and choose `bump`. The run prints the next version in its summary and builds everything without publishing (see [Dry run](#dry-run)).
3. Run it again with `dry_run` unchecked:
   - `plan` computes the version (`.github/ci-scripts/release-plan.sh`, unit tested by `task test-ci-scripts` in CI).
   - `create tag` pushes the annotated tag (authored by `github-actions[bot]`, unsigned) on the commit the run started on.
   - `release` builds and publishes in the same run (a tag pushed with `GITHUB_TOKEN` does not trigger a separate workflow run).
4. Wait for the run to finish before starting another: a second run started while one is active and another is pending replaces the pending run (one pending run per concurrency group). Runs are serialized and each computes its version after the previous one finishes.
5. On the first push to `ghcr.io`, the `semaphore` and `semaphore-cli` packages are created as private. In the organization package settings, link them to this repository and make them public.
6. Verify the artifacts (see [Verify a release](#verify-a-release)).

### Version rules

| Latest stable tag | `minor` | `major` |
|---|---|---|
| none (first release) | `v0.1.0` | `v0.1.0` |
| `v0.1.0` | `v0.2.0` | `v1.0.0` |
| `v1.2.3` | `v1.3.0` | refused (`v2.0.0`) |

- **No `patch` bump**: the selector offers `minor` (default) and `major` only; the plan script rejects `patch` and any other value.
- **Stable tags only**: only `vMAJOR.MINOR.PATCH` tags count as "latest"; pre-release tags are ignored.
- **First release**: always `v0.1.0` regardless of `bump` (see [First version](#first-version)).
- **Major >= 2 refused**: the Go module path has no `/vN` suffix, so a `v2+` tag would be invisible to the Go module proxy. Changing that requires an explicit module path change, not a workflow option.
- **Branch and tag guards**: releases run only from `main` (dry runs may start from any ref), and the workflow refuses to overwrite an existing tag.
- **Version source**: binaries read the tag via `-X main.version`; nothing else stores the version.

**Pre-releases** (`-alpha.N`, `-beta.N`, `-rc.N`, e.g., `v0.2.0-rc.1`) are not offered by the bump selector. An owner can push one manually (`git tag -s v0.2.0-rc.1 -m v0.2.0-rc.1 && git push origin v0.2.0-rc.1`); any pushed `v*` tag runs `release` directly, and any other suffix is refused. Pre-releases create a GitHub pre-release and push images tagged only with the exact version (e.g., `v0.2.0-rc.1` plus variant suffixes), never `latest` or rolling tags.

The first release has no previous tag, so the workflow writes a short fixed release note instead of a changelog (the git history includes commits from the project this one derives from). Later releases generate a changelog between tags.

## Dry run

In GitHub **Actions** -> **Release** -> **Run workflow**, keep `dry_run` checked (default) and pick a `bump`:

- Computes and prints the next version in the `plan` job summary without creating a tag.
- Builds snapshot archives for every platform, SBOMs, and all image variants for all platforms (QEMU), without publishing.
- Retains archives for 3 days as the `dry-run-release` workflow artifact.
- Every pull request also runs `goreleaser check` and a snapshot build of the FIPS binary for the current platform as part of `ci-ok`.

Locally (needs Docker with buildx, Go, goreleaser v2, syft):

```shell
export GOWORK=off
goreleaser check
goreleaser release --snapshot --clean --skip=publish,sign
```

The snapshot version is `<last tag + 1 patch>-next` (`0.0.1-next` when there is no tag). A
snapshot never pushes; the images stay in the local Docker daemon.

If the Go toolchain of your machine ships a corrupted `lib/fips140/*.zip`, the FIPS build fails
with a SHA-256 mismatch: use the official toolchain from go.dev.

## Verify a release

```shell
V=0.1.0
curl -fLO https://github.com/BlackbirdWorks/semaphore/releases/download/v$V/checksums.txt
curl -fLO https://github.com/BlackbirdWorks/semaphore/releases/download/v$V/semaphore_${V}_Linux_x86_64.tar.gz
sha256sum --ignore-missing -c checksums.txt
gh attestation verify semaphore_${V}_Linux_x86_64.tar.gz -R BlackbirdWorks/semaphore
tar xzf semaphore_${V}_Linux_x86_64.tar.gz && go version -m ./semaphore | grep -E 'main.version|GOFIPS140'
docker run --rm ghcr.io/blackbirdworks/semaphore:v$V --version
```

`semaphore --version` (or `-v`) and `semaphore-cli --version` print the name and version, commit,
build date, Go version and FIPS 140 mode, and exit without reading any configuration. The version
is also returned in the `X-SEMAPHORE-VERSION` response header and exposed by the `semaphore_build_info`
metric; `go version -m <binary>` shows the `-X main.version=` linker flag.

## Roll back or delete a bad release

- Mark the GitHub Release as a draft or delete it (`gh release delete v0.1.0 --yes`) and delete
  the tag (`git push origin :refs/tags/v0.1.0`). Do this only if nobody can have used it: the Go
  module proxy and the container registry cache what they saw.
- A Go module version, once fetched through `proxy.golang.org`, cannot be removed and the tag
  must never be moved. Publish a fixed release with the next `minor` bump (`v0.2.0`; there is no `patch`
  bump) and add a `retract` directive to `go.mod` for the bad one.
- Images: delete the bad version tags in the package settings (or `gh api -X DELETE
  /orgs/BlackbirdWorks/packages/container/semaphore/versions/<id>`), and re-point `latest` by
  re-running the workflow of the previous good tag (`gh workflow run release.yml --ref <tag> -f release_tag=<tag> -f dry_run=false`
  republishes it, tags included).
- A failed run that stopped half-way (images pushed, release not created): fix the cause and
  re-run the failed job, or re-publish the tag from the command line (`release_tag` runs must be started on the tag):

  ```shell
  gh workflow run release.yml --ref v0.1.0 -f release_tag=v0.1.0 -f dry_run=false
  ```

  - `gh workflow run --ref` accepts a tag; the workflow rejects `release_tag` runs started on any other ref because attestations record the run's source commit (running on `main` would record the wrong commit).
  - `bump` is ignored and the tag is never created or moved. If the failed run already created the tag, do not run another bump (that would create the next version). Use the CLI, as the web "Use workflow from" picker may not list tags.
  - Because a run on a tag executes `release.yml` as stored at that tag, fixes merged to `main` afterward do not apply. If `release.yml` itself is broken at that tag, fix it on `main` and cut a new release with a new bump instead of moving the tag.
  - GoReleaser replaces existing release files.

## First version

By owner decision, the first release is `v0.1.0`. When the repository has no stable `vMAJOR.MINOR.PATCH` tag, the workflow produces `v0.1.0` for any bump, so the first run can use the default choice.

Reasons:

- Semaphore has a different surface (renamed binary, image, metrics, config files), so reusing upstream `1.x` numbers would imply a drop-in successor.
- `0.x` signals that early releases do not yet carry a stable API promise.
- The Go module path (`github.com/blackbirdworks/semaphore`) stays free of a `/v2` suffix until `v1.0.0` is decided.
- A tag fetched by the Go module proxy can never be reused.

Supported platforms are 64-bit only (`amd64` and `arm64`); there are no 32-bit archives or images.

### Tags and snapshot versions

This repository has no tags on GitHub. A clone of the original project (or a local clone that fetched its tags) has hundreds (`v1.59.1`, etc.). Because GoReleaser derives snapshot versions from the nearest tag, such a clone builds `1.59.2-next`, while a clean checkout without tags builds `0.0.1-next`.

The release workflow reads only the tags in this repository's checkout (`v0.1.0` -> `0.1.0`, image tags `v0.1.0`, `v0.1`, `v0`, `latest`). Because the previous-tag lookup finds none, the first release uses a fixed note instead of a changelog (git history includes commits from the original project). Do not push or fetch the original project's tags into this repository.

## Secrets and permissions

The workflow uses `secrets.GITHUB_TOKEN` only:

- `plan` and `dry-run`: `contents: read`
- `create tag`: `contents: write` (pushes the tag; the token is passed to that single `git push` and not stored in the checkout)
- `release`: `contents: write` (creates release), `packages: write` (pushes images), `id-token: write`, `attestations: write` (provenance attestations)
- `deploy-docs`: runs in parallel with `release`, calling `.github/workflows/docs.yml` with `contents: read`, `pages: write`, and `id-token: write` to publish `docs/` to GitHub Pages for stable releases
- `ci.yml`: no secrets other than optional `CODECOV_TOKEN`
