# OpenTelemetry

## Overview

Export evaluation events as OpenTelemetry spans.

Each feature evaluation creates one OpenTelemetry span with attributes for non-sensitive evaluation details such as the flag key, evaluated variation, evaluation context key, and optional metadata.

This is a streaming exporter (not a bulk exporter): it creates one span per feature evaluation.

> [!NOTE]
> The exporter does not configure an OpenTelemetry SDK or `TracerProvider`. Initialize and configure OpenTelemetry (OTLP exporter, resource, sampler, etc.) in your application so spans reach your tracing backend.

## Behaviour

- Export mode: streaming (`IsBulk()` returns `false`).
- Each evaluation creates a span named `feature_flag.evaluate` with these attributes:
  - `feature_flag.key`, `feature_flag.user_key`, `feature_flag.context_kind`, `feature_flag.variation`, `feature_flag.default`, `feature_flag.version`, `feature_flag.source`
  - `feature_flag.value` (JSON-stringified when available)
  - `feature_flag.metadata.<key>` for each metadata entry
- When the default value is used, the span status is set to an error code to signal fallback usage.

## Configure the Go module

Provide an `&opentelemetryexporter.Exporter{}` in `ffclient.Config.DataExporter.Exporter` and optionally override the tracer name with `TracerName`:

```go
import (
    ffclient "github.com/blackbirdworks/semaphore"
    "github.com/blackbirdworks/semaphore/exporter/opentelemetryexporter"
)

// ... initialize your OpenTelemetry TracerProvider here (OTLP, resource, etc.) ...

cfg := ffclient.Config{
    // ... other config ...
    DataExporter: ffclient.DataExporter{
        Exporter: &opentelemetryexporter.Exporter{
            TracerName: "app-featureflags", // optional, default: "semaphore"
        },
    },
}

client, _ := ffclient.New(cfg)
defer client.Close()
```

| Field        | Mandatory | Default     | Description                                      |
|--------------|:---------:|-------------|--------------------------------------------------|
| `TracerName` | No        | `semaphore` | Optional override for the OpenTelemetry tracer name. |

## Configure the relay proxy

The relay proxy supports a per-flagset OpenTelemetry exporter (`kind: opentelemetry`) that creates one span per evaluation using `tracerName` and the process-wide OpenTelemetry `TracerProvider`. Configure the relay proxy's own OpenTelemetry SDK/exporter via the top-level `otel` section or environment variables (see the relay proxy observability docs).

```yaml
server:
  mode: http
  port: 1031

# Configure the relay proxy OpenTelemetry SDK/exporter (process-level)
otel:
  exporter:
    otlp:
      endpoint: http://otel-collector:4318
  resource:
    attributes:
      service.name: relay-proxy

# A flagset that exports evaluation events as OpenTelemetry spans
flagsets:
  - name: default
    apiKeys:
      - "my-api-key"
    retriever:
      kind: file
      path: ./flags.yaml
    exporters:
      - kind: opentelemetry
        tracerName: semaphore
```

Include `my-api-key` in your provider configuration or pass `X-API-Key: my-api-key` when making requests to this flagset.

Notes:

- This example uses the default OTLP HTTP/protobuf transport on port `4318`. To use gRPC, set `OTEL_EXPORTER_OTLP_PROTOCOL=grpc` in the relay proxy environment and change the endpoint to your collector's gRPC port (typically `4317`). The YAML `otel.exporter.otlp.protocol` field does not select the transport.
- Top-level `otel` config controls the OpenTelemetry SDK and OTLP exporter used by the proxy process; ensure it points to your collector or is configured via environment variables.
- The per-flagset exporter currently accepts only `tracerName` and reuses the process-global `TracerProvider`.

## Notes

- Configure an OpenTelemetry `TracerProvider` before evaluations run; otherwise spans are no-ops and will not be exported.
- The exporter sets the span timestamp from the original event creation time when available.
- Because values are JSON-marshaled, large values increase span payload size; prefer compact, non-sensitive metadata.
