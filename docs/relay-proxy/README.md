# Relay Proxy

The **relay proxy** is the stateless HTTP backend for Semaphore. It reads flag sets from a control plane store (S3 or a directory), keeps them in memory, and serves evaluations to any [OpenFeature](https://openfeature.dev) SDK using [OFREP](../openfeature/ofrep.md).

## Key traits

- **Stateless and lightweight**: single Go binary with no database required.
- **Always up to date**: polls the control plane manifest and applies a change after one poll, without a restart.
- **Drop-in OpenFeature backend**: use standard OpenFeature SDKs in any language without custom providers.

> [!NOTE]
> If you only need flag evaluation inside a Go service, you can embed the [Go module](../go-module/getting-started.md) directly instead of running the relay proxy.

## Next steps

- [Getting started](getting-started.md) — run the relay proxy locally in a few minutes.
- [Installation](install.md) — Docker images, release binaries, and FIPS 140-3 builds.
- [Bootstrap file reference](bootstrap-config.md) — connections, control plane, listeners, exporters and notifiers.
