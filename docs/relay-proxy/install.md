# Installation

## Build from source

Requires Go (see `go.mod` for the version).

```shell
git clone https://github.com/BlackbirdWorks/semaphore.git
cd go-feature-flag
make build-relayproxy   # binary: out/bin/relayproxy
```

## Docker

Build a local image from source:

```shell
make build-relayproxy-docker   # image: go-feature-flag:local
```

The relay proxy image is named `ghcr.io/blackbirdworks/semaphore`; the
Dockerfiles under `cmd/relayproxy/` and the Helm chart use that name. This repository
does not run a release pipeline, so build and publish the image yourself if you need
it in a registry.

## FIPS 140-3

If you need FIPS 140 validated cryptography, build the FIPS variant from source:

```shell
make build-relayproxy-fips
```

See [FIPS 140-3 mode](advanced-usage.md#fips-140-3-mode) for details on running and verifying it.

## Next steps

Once the relay proxy is running, connect your application with a standard
[OpenFeature SDK and the OFREP provider](../openfeature/ofrep.md).
