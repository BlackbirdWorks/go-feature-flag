# Rename to Semaphore

This project was named GO Feature Flag ("GOFF") before it became **Semaphore**. It is an independent derivative of
`GO Feature Flag` by Thomas Poignant (MIT); see [NOTICE](../../NOTICE)
and [UPSTREAM.md](../../UPSTREAM.md).

The wire protocol did not change: standard OpenFeature SDKs and GO Feature Flag OpenFeature providers keep working
against the relay proxy without changes. Operator- and client-facing names now use Semaphore names; legacy names are
still accepted (or emitted alongside new ones) unless the table says otherwise. The relay proxy reads one version 2
[bootstrap file](../relay-proxy/bootstrap-config.md): the legacy configuration file and environment names are gone.

When the relay proxy *reads* the legacy evaluation context key, it logs **one** warning per process. When both names are
set, the Semaphore name wins.

| Old name (GO Feature Flag) | New name (Semaphore) | Old name still accepted? |
|---|---|---|
| Go module `github.com/thomaspoignant/go-feature-flag` (and `/modules/core`) | `github.com/blackbirdworks/semaphore` (and `/modules/core`) | No: change your imports. The Go identifiers (`ffclient`, `GoFeatureFlag`, ...) are unchanged. |
| Relay proxy binary `go-feature-flag` (`go-feature-flag-relay-proxy`) | `semaphore` | No (release artifact name) |
| CLI binary `go-feature-flag-cli`, `goff` | `semaphore-cli` | No (release artifact name) |
| Binary path in the image `/go-feature-flag` | `/usr/local/bin/semaphore` (now the image `ENTRYPOINT`: arguments are passed directly, `docker run IMAGE --config=...`; use `--entrypoint` to run something else) | No: update any `command:` / `docker run ... /go-feature-flag` override; a `command:` that repeated the binary path must become `args:` without it (or be removed) |
| Image `thomaspoignant/go-feature-flag` | `ghcr.io/blackbirdworks/semaphore` | No (different registry) |
| Image `thomaspoignant/go-feature-flag-cli` | `ghcr.io/blackbirdworks/semaphore-cli` | No |
| Relay proxy config file `goff-proxy.<yaml\|toml\|json\|yml>`, folders `/goff/`, `/etc/opt/goff/`, the `GOFF` environment prefix | `--config` (or `SEMAPHORE_CONFIG`) of a version 2 bootstrap file | No: the relay reads one version 2 file, the legacy names are gone |
| Default flag file of the CLI `flags.goff.<ext>` | `flags.semaphore.<ext>` | Yes; warns once |
| Evaluation context key `gofeatureflag` (`gofeatureflag.flagList`, `.currentDateTime`, `.exporterMetadata`) | `semaphore` (same sub-keys) | Yes, field by field; `semaphore` wins; warns once |
| OFREP response metadata `gofeatureflag_cacheable` | `semaphore_cacheable` | Both are sent; `evaluation.disableSemaphoreMetadata: true` sends only the legacy one |
| Response header `X-GOFEATUREFLAG-VERSION` | `X-SEMAPHORE-VERSION` | Both are sent |
| Prometheus series `gofeatureflag_*` | `semaphore_*` | Opt in: `observability.metrics.legacyNames: true` exposes both |
| OpenTelemetry service name and tracer `go-feature-flag` | `semaphore` | Overridable (`otel.service.name`, `tracerName` of the OpenTelemetry exporter); the old default is gone |
| Slack notifier footer and Teams message title `GO Feature Flag` | `Semaphore` | No (text shown to people) |

## What did not change

- HTTP paths: `/v1/feature/...`, `/v1/allflags`, `/v1/data/collector`, `/ofrep/v1/...`, the websocket and SSE paths, `/health`,
  `/info`, `/metrics`, and `/admin/v1/...`.
- JSON field names of request and response bodies, and the flag file format.
- Exporter output for downstream consumers: file name defaults, data formats, and the `emitter` attribute
  (`GO Feature Flag`) added by the Pub/Sub and SQS exporters.
- Evaluation context attribute names used in your own targeting rules.

## Migrating step by step

1. Deploy the new binary or image.
2. Write the version 2 [bootstrap file](../relay-proxy/bootstrap-config.md) and pass it with `--config`.
3. Move your dashboards and alerts to the `semaphore_` series; set `observability.metrics.legacyNames: true` until complete.
4. Update Go imports:

```shell
sed -i 's#github.com/thomaspoignant/go-feature-flag#github.com/blackbirdworks/semaphore#g'
```
