# Export evaluation data

## Overview
Sometimes when you are using feature flags in your application, you may want to collect data on how the flags are being used.
This is the reason why Semaphore has the exporter concept, a mechanism that store an event for each evaluation of a flag.

## About exporters
To export evaluation data, you have to configure the target where you want to export the data.

Semaphore provides a list of built-in exporters that you can use out of the box. The goal is to let you choose the one that fits your needs the best.

During flag evaluation, the key, flag variation and other non-sensitive information used are collected and cached for a configurable period of time.
When the cache is full or the cache duration is reached, the exporter is called with the list of events to store it where you want.

You have 2 different ways to specify a exporter depending on if you are using the **GO Module** or the **Relay Proxy**.
In all the details pages of the exporters below you will have an example for both usages (relay proxy and Go module).

## Available Exporters
Bellow is the full list of exporters that are available in Semaphore.

- [AWS S3](aws-s3.md): Export evaluation data to a AWS S3 Bucket.
- [MinIO](minio.md): Export evaluation data to MinIO, or any other S3-compatible object store.
- [Azure Blob Storage](azure-blob-storage.md): Export evaluation data to an Azure Blob Storage.
- [File System](file.md): Export evaluation data to a directory in your file system.
- [Apache Kafka](kafka.md): Export evaluation data inside a Kafka topic.
- [AWS Kinesis](aws-kinesis.md): Export evaluation data inside a Kafka Kinesis stream.
- [Google Cloud Storage](google-cloud-storage.md): Export evaluation data to a Google Cloud Storage Bucket.
- [Google Cloud PubSub](google-cloud-pubsub.md): Export evaluation data inside a GCP PubSub topic.
- [Google Cloud BigQuery](google-cloud-bigquery.md): Export evaluation and tracking data to Google Cloud BigQuery.
- [AWS SQS](aws-sqs.md): Export evaluation data inside a AWS SQS queue.
- [AWS Redshift](aws-redshift.md): Export evaluation and tracking data to AWS Redshift.
- [Webhook](webhook.md): Export evaluation data by calling a HTTP Webhook.
- [Log](log.md): Export evaluation data inside the application logger.
- [OpenTelemetry](opentelemetry.md): Export evaluation events as OpenTelemetry spans.

## Custom exporter
If you have a specific use case that is not covered by the built-in exporters, you can create your own custom exporter.

To create a custom exporter you must have a `struct` that implements the exporter.Exporter interface.

```go
type Exporter interface {
  // Export will send the data to the exporter.
  Export(context.Context, *fflog.FFLogger, []ExportableEvent) error

	// IsBulk return false if we should directly send the data as soon as it is produced
	// and true if we collect the data to send them in bulk.
	IsBulk() bool
}
```

- `Export(...)` is called asynchronously with a list of `ExportableEvent` that have been collected.
   It is your responsibility to store them where you want.
- `IsBulk()` function should return false if the exporter can handle the results in stream mode.
   If you decide to manage it in streaming mode, everytime we call a variation the Export function will be called with only on event in the list.

> [!NOTE]
> If you think your custom exporter could be useful for others, feel free to open a [pull request](https://github.com/BlackbirdWorks/go-feature-flag/pulls) to add it to the list of available exporters.

> [!WARNING]
> If you are using a custom exporter, it is easy to use it for the GO Module, but if you want to use it in the relay proxy you will have to recompile it yourself.
