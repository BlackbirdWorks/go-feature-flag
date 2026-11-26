# Export Evaluation Data (Exporters)

## Overview

An **exporter** records flag evaluation and tracking events and sends them to external analytics, logging, or storage systems. Bulk exporters (`IsBulk() == true`) buffer events in memory and flush on a timer or buffer limit; streaming exporters (`IsBulk() == false`) send events immediately.

Every integration page below includes examples for both the **Relay Proxy** and the **Go module**.

## Available exporters

- [AWS S3](aws-s3.md): export events to an AWS S3 bucket.
- [MinIO](minio.md): export events to MinIO or any S3-compatible object store.
- [Azure Blob Storage](azure-blob-storage.md): export events to Azure Blob Storage.
- [File System](file.md): write events to a local directory.
- [Apache Kafka](kafka.md): publish events to a Kafka topic.
- [AWS Kinesis](aws-kinesis.md): publish events to an AWS Kinesis stream.
- [Google Cloud Storage](google-cloud-storage.md): export events to a GCS bucket.
- [Google Cloud Pub/Sub](google-cloud-pubsub.md): publish events to a GCP Pub/Sub topic.
- [Google Cloud BigQuery](google-cloud-bigquery.md): export evaluation and tracking data to BigQuery.
- [AWS SQS](aws-sqs.md): send events to an AWS SQS queue.
- [AWS Redshift](aws-redshift.md): export evaluation and tracking data to AWS Redshift.
- [Webhook](webhook.md): send events to an HTTP webhook.
- [Log](log.md): write events to the application logger.
- [OpenTelemetry](opentelemetry.md): export evaluation events as OpenTelemetry spans.

## Custom exporter

Implement `exporter.Exporter` to send events to a custom destination:

```go
type Exporter interface {
  // Export sends collected events to the destination.
  Export(context.Context, *slog.Logger, []ExportableEvent) error

  // IsBulk returns false to stream each event immediately, or true to batch events.
  IsBulk() bool
}
```

- `Export(...)` runs in the `AddEvent` caller's goroutine for non-bulk exporters (so a slow exporter delays that caller), and in a background goroutine for `QueueEvents` or bulk flushes.
- `IsBulk()` controls when flushing starts (`false` flushes immediately on `AddEvent`; `true` waits for `FlushInterval` or `MaxEventInMemory`).

> [!NOTE]
> Want to contribute an exporter? Open a [pull request](https://github.com/BlackbirdWorks/semaphore/pulls).

> [!WARNING]
> Custom exporters plug straight into the Go module, but require rebuilding the relay proxy binary to use them there.
