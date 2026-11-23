# Export evaluation data
The module supports different ways of exporting evaluation data.

To retrieve a file you need to provide an [exporter](https://pkg.go.dev/github.com/thomaspoignant/go-feature-flag/exporter/) in your `ffclient.Config{}` during the initialization.

## Available exporters:
- [AWS S3](../integrations/exporters/aws-s3.md)
- [MinIO](../integrations/exporters/minio.md)
- [Azure Blob Storage](../integrations/exporters/azure-blob-storage.md)
- [File System](../integrations/exporters/file.md)
- [Apache Kafka](../integrations/exporters/kafka.md)
- [AWS Kinesis](../integrations/exporters/aws-kinesis.md)
- [Google Cloud Storage](../integrations/exporters/google-cloud-storage.md)
- [Google Cloud PubSub](../integrations/exporters/google-cloud-pubsub.md)
- [Google Cloud BigQuery](../integrations/exporters/google-cloud-bigquery.md)
- [AWS SQS](../integrations/exporters/aws-sqs.md)
- [AWS Redshift](../integrations/exporters/aws-redshift.md)
- [Webhook](../integrations/exporters/webhook.md)
- [Log](../integrations/exporters/log.md)
- [OpenTelemetry](../integrations/exporters/opentelemetry.md)

> [!NOTE]
> In each exporter documentation, you will find the configuration needed to set up the exporter.
