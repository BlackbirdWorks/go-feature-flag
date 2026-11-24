# Releasing Semaphore

A release is cut from the GitHub UI: run the `Release` workflow (`.github/workflows/release.yml`) on `main`
and choose the bump (`patch`, `minor` or `major`). The workflow finds the latest stable tag, computes
the next version, creates and pushes the annotated tag, then builds and publishes that tag in the same run.
Nobody types a version or a tag by hand. It uses the repository `GITHUB_TOKEN` only: no other secret,
no other registry, nothing is pushed to a branch or to another repository.

Nothing has been released yet. Everything below is "available after the first release".

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

All images run as uid 65532 and have `/usr/local/bin/semaphore` as `ENTRYPOINT` (the CLI image has
`/semaphore-cli`): arguments are passed directly (`docker run IMAGE --config /path`), and `--entrypoint` overrides it. They carry the `org.opencontainers.image.*` labels (source =
this repository, license, version, revision). The images are not attested or signed; the archives are (see below).

Not published, on purpose, because the project owns no destination for them: Homebrew tap,
Scoop bucket, Docker Hub, AWS public ECR, npm, PyPI, Maven, NuGet, WebAssembly packages, a Helm
chart (the Helm chart of the original project was not carried over: the owner does not need one). No GPG signature either (the project has no signing key); use the attestations.

## Cut a release

1. Make sure `main` is green.
2. Actions, "Release", Run workflow, "Use workflow from": `main`. Leave `dry_run` checked and pick the `bump`:
   the run prints the next version in its summary and builds everything without publishing (see "Dry run").
3. Run it again with `dry_run` unchecked. The `plan` job computes the version, `create tag` pushes the annotated
   tag (made by `github-actions[bot]`, not signed: the project has no signing key) on the commit the run started
   on, and `release` publishes it. The release job runs in the same workflow run: a tag pushed with
   `GITHUB_TOKEN` does not start another run, which is why the tag is not a separate trigger here.
   The version logic is `.github/ci-scripts/release-plan.sh`, unit tested by `make test-ci-scripts` (run in CI).
4. Watch the run to its end before starting another: a second run started while one is active and another is
   pending replaces the pending run (one pending run per concurrency group). Runs are serialised, and each
   computes its version after the previous one finished.
5. The first push to `ghcr.io` creates the packages as private: in the organization package
   settings, link them to this repository and make them public. There are two: `semaphore`
   and `semaphore-cli`.
6. Verify the artifacts (below).

Version rules:

| Latest stable tag | `patch` | `minor` | `major` |
|---|---|---|---|
| none (first release) | `v0.1.0` | `v0.1.0` | `v0.1.0` |
| `v0.1.0` | `v0.1.1` | `v0.2.0` | `v1.0.0` |
| `v1.2.3` | `v1.2.4` | `v1.3.0` | refused (`v2.0.0`) |

- Only stable tags (`vMAJOR.MINOR.PATCH`) count as "latest"; pre-release tags are ignored.
- The first release is always `v0.1.0` whatever the bump (see "First version").
- A bump that would give major 2 or more is refused: the Go module path has no `/vN` suffix, so such a tag would be
  invisible to the Go module proxy. Changing that is a deliberate module path change, not a button.
- The release run starts only from `main` (a dry run may start from any ref). The workflow also refuses to
  overwrite an existing tag.
- The version is taken from the tag (the binaries get it through the `-X main.version` linker flag); nothing else carries it.

Pre-releases (`-alpha.N`, `-beta.N`, `-rc.N`, for example `v0.2.0-rc.1`) are not offered by the bump selector. An owner
can still push such a tag by hand (`git tag -s v0.2.0-rc.1 -m v0.2.0-rc.1 && git push origin v0.2.0-rc.1`): a
pushed `v*` tag runs the release job directly. Any other suffix is refused. A pre-release creates a GitHub
pre-release; its images get only the exact `v0.2.0-rc.1` tags (plus variant suffixes), never `latest` or the rolling tags.

The first release has no previous tag, so the workflow writes a short fixed release note instead
of a changelog (the git history also holds the commits of the project this one derives from).
Later releases get the generated changelog between tags.

## Dry run

Actions, "Release", Run workflow, keep `dry_run` checked (the default) and pick a `bump`. It computes and prints
the next version (job summary of `plan`), creates no tag, and builds the snapshot
archives for every platform, the SBOMs, all image variants for all platforms (QEMU), and publishes nothing; the archives are kept three days as the `dry-run-release` workflow
artifact. Every pull request also runs `goreleaser check` and a snapshot build of the FIPS binary for the current
platform as part of `ci-ok`.

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
  must never be moved. Publish a fixed `v0.1.1` and add a `retract` directive to `go.mod` for the
  bad one.
- Images: delete the bad version tags in the package settings (or `gh api -X DELETE
  /orgs/BlackbirdWorks/packages/container/semaphore/versions/<id>`), and re-point `latest` by
  re-running the workflow of the previous good tag (`workflow_dispatch` on that tag with `release_tag` set to it and
  `dry_run` unchecked republishes it, tags included).
- A failed run that stopped half-way (images pushed, release not created): fix the cause and
  re-run the failed job, or run the workflow with "Use workflow from" set to that tag (not `main`: the attestations take their source commit from the run, and the workflow refuses otherwise), `release_tag` set to the same tag and `dry_run` unchecked (the `bump` input is
  ignored; the tag is never created or moved; if the run had already created the tag, do not run a bump again, it would
  make the next version).
  GoReleaser replaces existing release files.

## First version

Decided by the owner: the first release is `v0.1.0`. When the repository has no stable `vMAJOR.MINOR.PATCH` tag, the
workflow produces `v0.1.0` for any bump, so the first run can be launched with the default choice.

Reasons: Semaphore is a different project with a different surface (renamed binary, image, metrics,
config files), so reusing upstream's `1.x` numbers would suggest a drop-in successor; `0.x` states
that the first releases are not yet a stable API promise; and the Go module path
(`github.com/blackbirdworks/semaphore`) stays free of a `/v2` suffix until a `v1.0.0` is decided.
A tag fetched by the Go module proxy can never be reused.

Supported platforms are 64-bit only (amd64 and arm64); there are no 32-bit archives or images.

### Tags and snapshot versions

This repository (GitHub) has no tags. A clone of the original project, or a local clone that fetched its
tags, has hundreds (`v1.59.1` and the like). GoReleaser derives the version of a snapshot from the
nearest tag, so such a clone builds `1.59.2-next` while a clean checkout without tags builds `0.0.1-next`.
The release workflow reads only the tags of this repository's checkout, so a repository with no tags starts at
`v0.1.0`. Neither matters for a release: a release uses the tag itself (`v0.1.0` gives `0.1.0`, image tags
`v0.1.0`, `v0.1`, `v0` and `latest`). The previous-tag lookup finds none on this repository, which is why the
workflow writes a fixed note for the first release instead of a changelog (the history contains the
commits of the original project). Do not push or fetch the original project's tags into this repository.

## Secrets and permissions

The workflow references `secrets.GITHUB_TOKEN` only. Jobs: `plan` and `dry-run` have `contents: read`;
`create tag` has `contents: write` (push the tag; the token is passed to that one `git push` and not stored in the checkout);
`release` has `contents: write` (create the release), `packages: write` (push images),
`id-token: write` and `attestations: write` (provenance attestations). `ci.yml` has no secret
other than the optional `CODECOV_TOKEN`.
