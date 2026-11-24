# Installation

## Build from source

Requires Go (see `go.mod` for the version).

```shell
git clone https://github.com/BlackbirdWorks/semaphore.git
cd semaphore
make build-relayproxy   # binary: out/bin/semaphore
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
linux/amd64, linux/arm64 and (not the FIPS ones) linux/arm/v7. The image runs as the non-root user 65532.

```shell
docker run -p 1031:1031 -v $(pwd):/semaphore:ro ghcr.io/blackbirdworks/semaphore:latest
```

Until then, build a local image from source:

```shell
make build-relayproxy-docker   # image: semaphore:local
```

The binary is `/usr/local/bin/semaphore` and the image sets it as `CMD` (not `ENTRYPOINT`, as before the rename), so
the relay proxy starts with no arguments and finds its configuration in `/semaphore/` (or the legacy `/goff/`).
To pass options, give the full command, which replaces the `CMD`:

```shell
docker run -p 1031:1031 -v $(pwd):/cfg:ro semaphore:local /usr/local/bin/semaphore --config=/cfg/semaphore-proxy.yaml
```

## FIPS 140-3

If you need FIPS 140 validated cryptography, use the `semaphore-fips` archive or the `-fips` image tag
of a release, or build the FIPS variant from source:

```shell
make build-relayproxy-fips
```

See [FIPS 140-3 mode](advanced-usage.md#fips-140-3-mode) for details on running and verifying it.

## Next steps

Once the relay proxy is running, connect your application with a standard
[OpenFeature SDK and the OFREP provider](../openfeature/ofrep.md).
