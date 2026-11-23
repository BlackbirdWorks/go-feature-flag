# Semaphore relay proxy

The relay proxy loads your feature flag configuration (from a file, an HTTP endpoint, a git
provider, a bucket, a database, ...) and exposes HTTP APIs to evaluate your flags. It speaks OFREP
(OpenFeature Remote Evaluation Protocol), so applications can use the standard OpenFeature SDKs.

## When should I use the relay proxy?
- You want to access your flags through an API instead of the Go module.
- Your application is not written in Go.
- You want to centralize the access to your flag configuration.

## Quick start

```shell
make build-relayproxy        # from the repository root; binary: out/bin/relayproxy
./out/bin/relayproxy --config=/path/to/goff-proxy.yaml
```

If `--config` is not provided, the relay proxy looks for a file named `goff-proxy.yaml` in the current
folder, `/goff/` and `/etc/opt/goff/`.

Documentation:
- [Getting started](../../docs/relay-proxy/getting-started.md)
- [Installation](../../docs/relay-proxy/install.md)
- [Configuration reference](../../docs/relay-proxy/configuration.md)
- [API endpoints](../../docs/relay-proxy/endpoints.md)

## Container image
`make build-relayproxy-docker` builds a local image from source. Published images use the name
`ghcr.io/blackbirdworks/semaphore`.
