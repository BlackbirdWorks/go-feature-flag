# Observability

## Overview
Observability is crucial to know how your applications are working. And this is probably something you want to have for
your feature flag solution as well.
This is why Semaphore relay-proxy offers out of the box some ways of monitor what is happening.

## Logging
Semaphore relay proxy logs through the standard [`log/slog`](https://pkg.go.dev/log/slog) package, with its own handler (`cmdhelpers/cmdlog`) that writes the `json` and `logfmt` formats without allocating.

By default, the relay proxy is logging to the standard output with a JSON format in level `INFO`.

You can change the log level by setting the `logLevel` in your configuration file and you can choose the log format by setting the `logFormat` options.

```yaml
#...
logLevel: ERROR
logFormat: json
#...
```
| Field name |    Mandatory     | Type   | Default    | Description                                                                                                                                                                                                                                                     |
|------------|:----------------:|--------|------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| logLevel   | No | string | **`INFO`** | The log level to use for the relay proxy.<br/> Available values are `ERROR`, `WARN`, `INFO`, `DEBUG`.                                                                                                                                                           |
| logFormat  | No | string | **`json`** | <p>The format to use for structured logs from the relay proxy.<br/>Valid values are `json` and `logfmt`.</p><p><ul><li>**json**: Will output the logs in `JSON` format (default).</li><li>**logfmt**: Will output the log as text in the console.</li></ul></p> |

### Log output

A `json` record is one line with the keys `level` (`debug`, `info`, `warn`, `error` or `fatal`), `ts` (seconds since the
epoch, as a decimal number), `caller` (`directory/file.go:line`), `msg`, then the attributes of the record.
A `logfmt` record has the same fields as `ts=... level=... caller=... msg=... key=value`. Durations are written in seconds.
Values that contain a space, a `=` or a `"` are quoted in `logfmt`.

The lines written while a request is served (the access record, the errors of the handlers, the recovered panics)
carry the attributes of the request: `method` (the access record has it in its `request` field), `request_id` (the value
of the `X-Request-ID` header of the request, when it has one), `flagset` (the name of the flag set, once the API key is
resolved, for the endpoints that look it up) and, when [tracing](#tracing) is on, `trace_id` and `span_id`.

Secrets are never logged: the configuration is logged redacted, and the values of the API keys, tokens and connection
strings are replaced by `[REDACTED]`.

Differences with the logs of the versions that used Zap:

- the records of level `error` no longer carry a `stacktrace` field, and repeated identical records are no longer sampled;
- in `logfmt`, a value that is a map or a structure is written as quoted JSON (it used to be written as an empty value
  followed by a `...Error="unsupported value type"` field);
- in `json`, the configuration logged at the `debug` level when a new configuration is rejected is a JSON object, it used
  to be a string containing JSON.

## Tracing

The relay proxy will attempt to send traces to an OpenTelemetry collector or
compatible agent if an OpenTelemetry exporter endpoint is configured.

To set the endpoint, set the `OTEL_EXPORTER_OTLP_ENDPOINT` environment variable
or set `otel.exporter.otlp.endpoint` in the configuration file.

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

See [the OpenTelemetry documentation](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/) for more information.

All your requests will be traced and sent to the collector with the service name **`semaphore`**.

> [!WARNING]
> The metrics are sent to the same OTLP endpoint as the traces. A backend that only accepts traces (Jaeger, for
> example, answers `404` to the metrics) rejects them: the relay proxy logs an `OTel error` once per export
> interval (60 seconds by default), nothing else is affected, the requests included. Set `OTEL_METRICS_EXPORTER=none`
> to turn the metrics export off, or send everything to a collector that accepts both.

> [!NOTE]
> If you want to try the OpenTelemetry integration locally, follow this [example](https://github.com/BlackbirdWorks/semaphore/tree/main/cmd/relayproxy/testdata/opentelemetry)
> to set up Jaeger and see your traces.

### OpenTelemetry metrics

When OpenTelemetry is configured (an OTLP endpoint is set, and `otel.sdk.disabled` is not), the relay proxy also exports
OpenTelemetry **metrics**, to the same endpoint and with the same service name as the traces:

- the **Go runtime** metrics (`go.memory.used`, `go.memory.allocated`, `go.goroutine.count`, `go.processor.limit`,
  `go.schedule.duration`, `go.config.gogc`, ...);
- the **HTTP server** metrics of the requests (`http.server.request.duration`, ...), with the `http.route` attribute.

The protocol, the endpoint and the headers follow the standard `OTEL_EXPORTER_OTLP_*` environment variables, the export
interval is the default of the SDK, and `OTEL_METRICS_EXPORTER=none` stops the export (needed for a trace-only
backend, see the warning above). An export that fails is logged, it never affects a request. Without OpenTelemetry
configuration nothing is created and the requests pay nothing. This does not change the Prometheus
[`/metrics`](#metrics) endpoint, which keeps its metrics and labels.

---

## Monitoring

The **relay proxy** offers some endpoints for you to be able to see how it behaves.

### `/health`
Making a **GET** request to the URL path `/health` will tell you if the relay proxy is ready to
serve traffic.

This is useful especially for loadbalancer to know that they can send traffic to the service.

### `/info`
Making a **GET** request to the URL path `/info` will give you information about the actual state
of the relay proxy.

### `/metrics`
This endpoint is providing metrics about the relay proxy in the prometheus format.

> [!NOTE]
> The relay proxy supports [Prometheus native histograms](https://prometheus.io/docs/specs/native_histograms/) for improved latency distribution precision. When scraping with Prometheus (especially using protobuf format), histogram metrics will include both classic bucket data and native histogram data for higher-fidelity percentile calculations. Classic histogram buckets are preserved for backward compatibility.

All the series of the relay proxy are prefixed with `semaphore_`. The prefix used by Semaphore was
`gofeatureflag_`: set [`exposeLegacyMetrics`](configuration.md#exposelegacymetrics) to also expose every series under
that prefix while you migrate your dashboards and alerts.

#### Loaded flags
The gauge `semaphore_flag_loaded` exposes one series per flag currently loaded in the relay proxy cache,
with the labels `flagset`, `flag_name` and `disabled`. It is computed from the cache on every scrape, so a
deleted flag disappears on the next scrape after the cache refresh. The `disabled` label reflects the effective
state at scrape time, including due `scheduledRollout` steps and ended `experimentation` windows.

```promql
# all flags currently served, per flagset
sum by (flagset) (semaphore_flag_loaded)

# flags loaded but disabled
semaphore_flag_loaded{disabled="true"}
```

---

## Use specific port for the monitoring
You can configure a different port for the monitoring endpoints.   
This is useful if you want to expose the monitoring endpoints on a different port than the main service.

```yaml
# ...
monitoringPort: 1032 # only the monitoring endpoints will be exposed on this port
# ...
```

> [!NOTE]
> By default the monitoring endpoints are exposed on the same port as the main service.
