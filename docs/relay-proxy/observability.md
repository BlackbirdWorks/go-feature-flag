# Observability

## Overview

The Semaphore relay proxy provides built-in structured logging, OpenTelemetry tracing and metrics, and Prometheus/health monitoring endpoints.

## Logging

The relay proxy logs through Go's [`log/slog`](https://pkg.go.dev/log/slog) package using a custom zero-allocation handler (`cmdhelpers/cmdlog`) supporting `json` and `logfmt`.

By default, it logs to stdout in `json` format at `INFO` level. Configure this via `logLevel` and `logFormat`:

```yaml
# ...
logLevel: ERROR
logFormat: json
# ...
```

| Field name | Mandatory | Type | Default | Description |
|---|---|---|---|---|
| `logLevel` | No | string | **`INFO`** | Log level (`ERROR`, `WARN`, `INFO`, `DEBUG`). |
| `logFormat` | No | string | **`json`** | Structured log format: `json` (JSON lines) or `logfmt` (`key=value` text). |

### Log output

- **`json` records**: single line with `level` (`debug`, `info`, `warn`, `error`, or `fatal`), `ts` (decimal seconds since epoch), `caller` (`directory/file.go:line`), `msg`, and record attributes.
- **`logfmt` records**: `ts=... level=... caller=... msg=... key=value` (durations in seconds; values with spaces, `=`, or `"` are quoted).
- **Request attributes**: access logs, handler errors, and recovered panics include `method` (inside `request` on access logs), `request_id` (from `X-Request-ID` when present), `flagset` (after API key resolution on endpoints that look it up), and `trace_id` / `span_id` when [tracing](#tracing) is enabled.
- **Redaction**: configuration logs redact secrets, replacing API keys, tokens, and connection strings with `[REDACTED]`.

Differences from older Zap-based versions:

- `error` records no longer include a `stacktrace` field, and repeated identical records are no longer sampled.
- In `logfmt`, map or struct values render as quoted JSON instead of an empty value with `...Error="unsupported value type"`.
- In `json`, rejected configurations logged at `debug` level appear as a JSON object instead of a JSON-encoded string.

## Tracing

When an OpenTelemetry exporter endpoint is set (`OTEL_EXPORTER_OTLP_ENDPOINT` or `otel.exporter.otlp.endpoint`), the relay proxy sends traces to your OpenTelemetry collector or compatible agent with service name **`semaphore`**.

### Environment variable

```shell
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
```

### Configuration file

```yaml
# ...
otel:
  exporter:
    otlp:
      endpoint: "http://localhost:4317"
```

See the [OpenTelemetry SDK environment variables docs](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/) for additional options.

> [!WARNING]
> OpenTelemetry metrics use the same OTLP endpoint as traces. Trace-only backends (such as Jaeger) return `404` for metrics, causing an `OTel error` log once per export interval (60s by default) without affecting requests. Set `OTEL_METRICS_EXPORTER=none` to disable metrics export, or send telemetry to a collector that supports both.

> [!NOTE]
> Follow the [OpenTelemetry example](https://github.com/BlackbirdWorks/semaphore/tree/main/cmd/relayproxy/testdata/opentelemetry) to run Jaeger locally and inspect traces.

### OpenTelemetry metrics

When OpenTelemetry is configured (an OTLP endpoint is set and `otel.sdk.disabled` is not), the relay proxy also exports OpenTelemetry **metrics** to the same endpoint under service name `semaphore`:

- **Go runtime metrics**: `go.memory.used`, `go.memory.allocated`, `go.goroutine.count`, `go.processor.limit`, `go.schedule.duration`, `go.config.gogc`, etc.
- **HTTP server metrics**: `http.server.request.duration`, etc., with the `http.route` attribute.

Protocol, endpoint, and headers use standard `OTEL_EXPORTER_OTLP_*` environment variables, with the SDK's default export interval. Set `OTEL_METRICS_EXPORTER=none` for trace-only backends. Export failures are logged and never block requests; when OpenTelemetry is unconfigured, nothing is initialized and requests incur zero overhead. This does not change the Prometheus [`/metrics`](#metrics) endpoint.

---

## Monitoring

### `/health`

`GET /health` reports whether the relay proxy is ready to serve traffic (useful for load balancer readiness checks).

### `/info`

`GET /info` returns the current state of the relay proxy.

### `/metrics`

`GET /metrics` exposes relay proxy metrics in Prometheus format.

> [!NOTE]
> The relay proxy supports [Prometheus native histograms](https://prometheus.io/docs/specs/native_histograms/) for higher-precision latency percentiles when scraped with Prometheus (especially over protobuf), while preserving classic histogram buckets for backward compatibility.

All relay proxy series use the `semaphore_` prefix. To also emit series under the legacy `gofeatureflag_` prefix while migrating dashboards and alerts, set [`exposeLegacyMetrics`](configuration.md#exposelegacymetrics).

#### Loaded flags

The `semaphore_flag_loaded` gauge exposes one series per flag currently in the cache with labels `flagset`, `flag_name`, and `disabled`. It is computed on every scrape: deleted flags disappear after cache refresh, and `disabled` reflects the effective state at scrape time (including due `scheduledRollout` steps and ended `experimentation` windows).

```promql
# all flags currently served, per flagset
sum by (flagset) (semaphore_flag_loaded)

# flags loaded but disabled
semaphore_flag_loaded{disabled="true"}
```

---

## Use a dedicated monitoring port

Set `monitoringPort` to serve `/health`, `/info`, and `/metrics` on a separate port from the main API:

```yaml
# ...
monitoringPort: 1032 # only the monitoring endpoints will be exposed on this port
# ...
```

> [!NOTE]
> By default, monitoring endpoints are served on the same port as the main service.
