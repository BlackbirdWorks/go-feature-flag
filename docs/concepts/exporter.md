# Exporters

## Overview

An **exporter** sends flag evaluation and tracking events to external systems for analytics, monitoring, and debugging.

## Why use exporters

- **Analytics & A/B testing**: track which flags are evaluated, which variations users receive, and in what context.
- **Debugging**: inspect evaluation history to diagnose unexpected flag behavior.
- **Observability**: stream events into existing logging, warehouse, or OpenTelemetry pipelines.

## Synchronous vs. asynchronous exporters

- **Synchronous**: send events immediately after evaluation (low-latency queues and streams).
  - Apache Kafka, AWS Kinesis, Google Cloud Pub/Sub, AWS SQS, Webhook, Log, OpenTelemetry
- **Asynchronous**: buffer events in memory and flush in batches on an interval or size limit.
  - AWS S3, MinIO, Azure Blob Storage, File System, Google Cloud Storage

## Supported exporters

- AWS S3
- MinIO
- Azure Blob Storage
- File System
- Apache Kafka
- AWS Kinesis
- Google Cloud Storage
- Google Cloud Pub/Sub
- Google Cloud BigQuery
- AWS SQS
- AWS Redshift
- Webhook
- Log
- OpenTelemetry

See [Configure exporters](../integrations/exporters/README.md) for setup examples.

## Feature vs. tracking exporters

Set `eventType` on each exporter to choose which events it receives:

- **`"feature"`** (default): one event per flag evaluation (flag key, variation, `userKey`, source). See [Flag usage tracking](../tracking/flag-usage-tracking.md).
- **`"tracking"`**: custom outcome events sent via the [OpenFeature Tracking API](../tracking/tracking-api.md). Support depends on the exporter backend (for example, `OpenTelemetry` skips tracking events).

You can configure two exporter entries pointing at the same backend—one for `"feature"` and one for `"tracking"`.

## Event payload

Each exported feature event includes:

- Flag key and returned variation
- `userKey` and `contextKind` (full evaluation-context attributes are exported only on tracking events)
- Evaluation source (for example, `SERVER` or `PROVIDER_CACHE`) and exporter metadata
