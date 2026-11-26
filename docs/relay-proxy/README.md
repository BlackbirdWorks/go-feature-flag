# Relay Proxy

The **relay proxy** is the stateless HTTP backend for Semaphore. It loads flag configurations from your retrievers, keeps them in memory, and serves evaluations to any [OpenFeature](https://openfeature.dev) SDK using [OFREP](../openfeature/ofrep.md).

## Key traits

- **Stateless and lightweight**: single Go binary with no database required.
- **Always up to date**: polls your configured sources on an interval (or refreshes on demand via the admin API).
- **Drop-in OpenFeature backend**: use standard OpenFeature SDKs in any language without custom providers.

> [!NOTE]
> If you only need flag evaluation inside a Go service, you can embed the [Go module](../go-module/getting-started.md) directly instead of running the relay proxy.

## Next steps

- [Getting started](getting-started.md) — run the relay proxy locally in a few minutes.
- [Installation](install.md) — Docker images, release binaries, and FIPS 140-3 builds.
- [Configuration reference](configuration.md) — all proxy, retriever, exporter, and notifier options.
