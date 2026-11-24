# Rename to Semaphore

This project was named GO Feature Flag ("GOFF") before it became **Semaphore**. It is an independent derivative of
[GO Feature Flag](https://github.com/thomaspoignant/go-feature-flag) by Thomas Poignant (MIT), see [NOTICE](../../NOTICE)
and [UPSTREAM.md](../../UPSTREAM.md).

The wire protocol did not change: the standard OpenFeature SDKs and the GO Feature Flag OpenFeature providers keep working
against the relay proxy without any change. Everything an operator or a client can see has a Semaphore name; the old name is
still accepted (or emitted next to the new one) unless the table says otherwise. When you use an old name that is *read*
by the relay proxy (configuration file, `GOFF` environment prefix, evaluation context key), the relay proxy logs **one**
warning per process and per kind of name, never one per request. When both names are set, the Semaphore one wins.

| Old name (GO Feature Flag) | New name (Semaphore) | Old name still accepted? |
|---|---|---|
| Go module `github.com/thomaspoignant/go-feature-flag` (and `/modules/core`) | `github.com/blackbirdworks/semaphore` (and `/modules/core`) | No: change your imports. The Go identifiers (`ffclient`, `GoFeatureFlag`, ...) are unchanged. |
| Relay proxy binary `go-feature-flag` (`go-feature-flag-relay-proxy`) | `semaphore` | No (release artifact name) |
| CLI binary `go-feature-flag-cli`, `goff` | `semaphore-cli` | No (release artifact name) |
| Binary path in the image `/go-feature-flag` | `/usr/local/bin/semaphore` (now the image `ENTRYPOINT`: arguments are passed directly, `docker run IMAGE --config=...`; use `--entrypoint` to run something else) | No: update any `command:` / `docker run ... /go-feature-flag` override; a `command:` that repeated the binary path must become `args:` without it (or be removed) |
| Image `thomaspoignant/go-feature-flag` | `ghcr.io/blackbirdworks/semaphore` | No (different registry) |
| Image `thomaspoignant/go-feature-flag-cli` | `ghcr.io/blackbirdworks/semaphore-cli` | No |
| Relay proxy config file `goff-proxy.<yaml\|toml\|json\|yml>` | `semaphore-proxy.<yaml\|toml\|json\|yml>` | Yes, searched after the new name in every folder; warns once |
| Config folders `/goff/`, `/etc/opt/goff/` | `/semaphore/`, `/etc/opt/semaphore/` | Yes (searched after the new folders) |
| Default flag file of the CLI `flags.goff.<ext>` | `flags.semaphore.<ext>` | Yes; warns once |
| Environment variables, no prefix (`SERVER_PORT`, ...) | unchanged; plus `SEMAPHORE_SERVER_PORT` | Yes, both forms work and `SEMAPHORE_` wins |
| `envVariablePrefix: GOFF_` (or any other prefix) | `SEMAPHORE_` is always read | Yes; a prefix starting with `GOFF` warns once |
| Evaluation context key `gofeatureflag` (`gofeatureflag.flagList`, `.currentDateTime`, `.exporterMetadata`) | `semaphore` (same sub-keys) | Yes, field by field; `semaphore` wins; warns once |
| OFREP response metadata `gofeatureflag_cacheable` | `semaphore_cacheable` | Both are sent; `disableSemaphoreMetadata: true` sends only the legacy one |
| Response header `X-GOFEATUREFLAG-VERSION` | `X-SEMAPHORE-VERSION` | Both are sent; `disableVersionHeader: true` removes both |
| Prometheus series `gofeatureflag_*` | `semaphore_*` | Opt in: `exposeLegacyMetrics: true` exposes both |
| OpenTelemetry service name and tracer `go-feature-flag` | `semaphore` | Overridable (`otel.service.name`, `tracerName` of the OpenTelemetry exporter); the old default is gone |
| Slack notifier footer and Teams message title `GO Feature Flag` | `Semaphore` | No (text shown to people) |
| Helm chart config key `goff-proxy.yml` mounted in `/goff` | `semaphore-proxy.yml` in `/semaphore` | The relay proxy reads both folders |

## What did not change

- HTTP paths: `/v1/feature/...`, `/v1/allflags`, `/v1/data/collector`, `/ofrep/v1/...`, the websocket and SSE paths, `/health`,
  `/info`, `/metrics`, `/admin/v1/...`.
- JSON field names of the request and response bodies, and the flag file format.
- What exporters write for downstream consumers: file name defaults, the data formats, and the `emitter` attribute
  (`GO Feature Flag`) that the Pub/Sub and SQS exporters add to their messages.
- The evaluation context attribute names you use in your own targeting rules.

## Migrating step by step

1. Deploy the new binary or image. Nothing else has to change: you only see the warnings that tell you which old names
   are still in use.
2. Rename the configuration file to `semaphore-proxy.yaml` (or keep passing `--config`).
3. If you set `envVariablePrefix: GOFF_`, switch your variables to `SEMAPHORE_` or to the unprefixed form.
   A Kubernetes Service named `semaphore` makes Kubernetes inject `SEMAPHORE_SERVICE_HOST`, `SEMAPHORE_PORT=tcp://...` and
   similar variables into the pods of its namespace. The relay proxy ignores the variables that have the name and the
   value of such a service link (their names are logged at the debug level), and the Helm chart sets
   `enableServiceLinks: false`. Your own settings, such as `SEMAPHORE_SERVER_PORT=1031`, are read as usual.
4. Move your dashboards and alerts to the `semaphore_` series; set `exposeLegacyMetrics: true` until it is done.
5. Update the Go imports with `sed -i 's#github.com/thomaspoignant/go-feature-flag#github.com/blackbirdworks/semaphore#g'`.
