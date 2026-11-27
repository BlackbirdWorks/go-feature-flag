# Installation

## Build from source

Requires Go (see `go.mod` for the version) and [Task](https://taskfile.dev).

```shell
git clone https://github.com/BlackbirdWorks/semaphore.git
cd semaphore
task build-relayproxy   # binary: out/bin/semaphore
```

## Release archives (available after the first release)

Archives for Linux, macOS and Windows are attached to each
[GitHub Release](https://github.com/BlackbirdWorks/semaphore/releases):
`semaphore_<version>_<Os>_<arch>.tar.gz` (`.zip` on Windows), with `checksums.txt`, an SBOM per
archive and a build provenance attestation. See [Releasing](../RELEASING.md#verify-a-release)
to verify them.

## Docker

Images are published to `ghcr.io/blackbirdworks/semaphore` (available after the first release):
`:v<version>`, `:latest` (distroless), `-bookworm` / `-trixie` (Debian slim) and `-fips` variants, for
linux/amd64 and linux/arm64 (64-bit only; there are no 32-bit builds). The image runs as the non-root user 65532.

```shell
docker run -p 1031:1031 -v $(pwd):/semaphore:ro ghcr.io/blackbirdworks/semaphore:latest
```

Until then, build a local image from source:

```shell
task build-relayproxy-docker   # image: semaphore:local
```

The image sets `/usr/local/bin/semaphore` as `ENTRYPOINT`. With no arguments, it needs `--config` (or `SEMAPHORE_CONFIG`) with the path of a version 2 bootstrap file; any arguments are passed directly to `semaphore`. Use `--entrypoint` to override it. Pass options as flags:

```shell
docker run -p 1031:1031 -v $(pwd):/cfg:ro semaphore:local --config=/cfg/bootstrap.yaml
```

## FIPS 140-3

If you need FIPS 140 validated cryptography, use the `semaphore-fips` archive or the `-fips` image tag
of a release, or build the FIPS variant from source:

```shell
task build-relayproxy-fips
```

See [FIPS 140-3 mode](advanced-usage.md#fips-140-3-mode) for details on running and verifying it.

## Check the version

```shell
semaphore --version        # also -v; semaphore-cli --version for the CLI
docker run --rm ghcr.io/blackbirdworks/semaphore:latest --version
```

Prints the version, commit, build date, Go version, and FIPS 140 mode, then exits without loading any configuration.

## Next steps

Once the relay proxy is running, connect your application with a standard
[OpenFeature SDK and the OFREP provider](../openfeature/ofrep.md).
