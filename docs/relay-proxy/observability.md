# Observability

## Overview

The Semaphore relay proxy provides built-in structured logging, OpenTelemetry tracing and metrics, and Prometheus/health monitoring endpoints.

## Logging

The relay proxy logs through Go's [`log/slog`](https://pkg.go.dev/log/slog) package using a custom zero-allocation handler (`cmdhelpers/cmdlog`) supporting `json` and `logfmt`.

By default, it logs to stdout in `json` format at `info` level. Configure this in the [bootstrap file](bootstrap-config.md#observability):

```yaml
observability:
  logging:
    level: error
    format: json
```

| Key | Type | Default | Allowed |
|---|---|---|---|
| `observability.logging.level` | string | `info` | `debug`, `info`, `warn`, `error` |
| `observability.logging.format` | string | `json` | `json` (JSON lines) or `text` (`key=value` logfmt lines) |

The flags `--log-level` and `--log-format`, and the variables `SEMAPHORE_LOG_LEVEL` and `SEMAPHORE_LOG_FORMAT`, override the file.

### Log output

- **`json` records**: single line with `level` (`debug`, `info`, `warn`, `error`, or `fatal`), `ts` (decimal seconds since epoch), `caller` (`directory/file.go:line`), `msg`, and record attributes.
- **`text` records (logfmt)**: `ts=... level=... caller=... msg=... key=value` (durations in seconds; values with spaces, `=`, or `"` are quoted).
- **Request attributes**: access logs, handler errors, and recovered panics include `method` (inside `request` on access logs), `request_id` (from `X-Request-ID` when present), `flagset` (after API key resolution on endpoints that look it up), and `trace_id` / `span_id` when [tracing](#tracing) is enabled.
- **Redaction**: configuration logs redact secrets, replacing API keys, tokens, and connection strings with `[REDACTED]`.

Differences from older Zap-based versions:

- `error` records no longer include a `stacktrace` field, and repeated identical records are no longer sampled.
- In `logfmt`, map or struct values render as quoted JSON instead of an empty value with `...Error="unsupported value type"`.
- In `json`, rejected configurations logged at `debug` level appear as a JSON object instead of a JSON-encoded string.

## Tracing

When an OpenTelemetry exporter endpoint is set (`observability.tracing.otlpEndpoint` or `OTEL_EXPORTER_OTLP_ENDPOINT`), the relay proxy sends traces to your OpenTelemetry collector or compatible agent with service name **`semaphore`**.

### Environment variable

```shell
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
```

### Bootstrap file

```yaml
observability:
  tracing:
    otlpEndpoint: "localhost:4317"
    serviceName: semaphore
    sampler: parentbased_traceidratio
```

The sampler is given to the SDK through `OTEL_TRACES_SAMPLER` when that variable is not set; see the
[keys](bootstrap-config.md#observability).

See the [OpenTelemetry SDK environment variables docs](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/) for additional options.

> [!WARNING]
> OpenTelemetry metrics use the same OTLP endpoint as traces. Trace-only backends (such as Jaeger) return `404` for metrics, causing an `OTel error` log once per export interval (60s by default) without affecting requests. Set `OTEL_METRICS_EXPORTER=none` to disable metrics export, or send telemetry to a collector that supports both.

> [!NOTE]
> Follow the [OpenTelemetry example](https://github.com/BlackbirdWorks/semaphore/tree/main/cmd/relayproxy/testdata/opentelemetry) to run Jaeger locally and inspect traces.

### OpenTelemetry metrics

When OpenTelemetry is configured (an OTLP endpoint is set and `OTEL_SDK_DISABLED` is not `true`), the relay proxy also exports OpenTelemetry **metrics** to the same endpoint under service name `semaphore`:

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

All relay proxy series use the `semaphore_` prefix. To also emit series under the legacy `gofeatureflag_` prefix while migrating dashboards and alerts, set [`observability.metrics.legacyNames`](bootstrap-config.md).

The metrics of the configuration (generations, reloads, rejected flags, objects by state), the reload log line and the probes are on the [probes page](probes.md#metrics).

---

## Use a dedicated monitoring port

Give the relay an `ops` listener to serve `/health`, `/info`, `/metrics` and the probes on a separate port from the main API:

```yaml
version: 2
server:
  listeners:
    - {name: api, address: ":1031"}
    - {name: ops, address: ":1032", role: ops}
```

> [!NOTE]
> Without an `ops` listener, the monitoring endpoints are served on the `api` listener.
