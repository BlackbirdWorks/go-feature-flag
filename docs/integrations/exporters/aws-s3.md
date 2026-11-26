# AWS S3

## Overview

The S3 exporter collects events and writes a new file to an AWS S3 bucket each time `FlushInterval` or `MaxEventInMemory` is reached.

> [!NOTE]
> If an AWS S3 upload fails, events remain in memory and are retried on the next `FlushInterval` or `MaxEventInMemory` flush.

## Configure the relay proxy

Add the following configuration to your relay proxy configuration file:

```yaml
# ...
exporters:
  - kind: s3
    bucket: evaluation-data-bucket
# ...
```

> [!NOTE]
> Provide AWS credentials using standard environment variables:
>
> ```shell
> export AWS_SECRET_ACCESS_KEY=xxxx
> export AWS_ACCESS_KEY_ID=xxx
> export AWS_DEFAULT_REGION=eu-west-1
> ```

| Field name                | Mandatory | Type   | Default                                                                                                                            | Description                                                                                                                                                                                                                        |
|---------------------------|:---------:|--------|------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `kind`                    | Yes       | string | **none**                                                                                                                           | Value should be `s3`.<br/>_This field is mandatory and describes which exporter you are using._                                                                                                                                      |
| `bucket`                  | Yes       | string | **none**                                                                                                                           | Name of your S3 bucket.                                                                                                                                                                                                            |
| `flushInterval`           | No        | int    | `60000`                                                                                                                            | Interval in milliseconds before flushing events (flushes earlier if `maxEventInMemory` is reached first).                                                                                                                          |
| `maxEventInMemory`        | No        | int    | `100000`                                                                                                                           | Maximum number of events buffered in memory before flushing.                                                                                                                                                                       |
| `format`                  | No        | string | `JSON`                                                                                                                             | Output file format: `JSON`, `CSV`, or `Parquet`.                                                                                                                                                                                   |
| `filename`                | No        | string | `flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}`                                                                       | Go template for exported filenames. Available fields: `{{ .Hostname}}`, `{{ .Timestamp}}`, and `{{ .Format}}`.                                                                                                                     |
| `csvTemplate`             | No        | string | `{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};{{ .Default}};{{ .Source}}\n` | Go template for CSV rows (ignored for other formats). See [`exporter/feature_event.go`](https://github.com/BlackbirdWorks/semaphore/blob/main/exporter/feature_event.go) for available fields.                                      |
| `path`                    | No        | string | **bucket root level**                                                                                                              | Directory path inside the S3 bucket.                                                                                                                                                                                               |
| `parquetCompressionCodec` | No        | string | `SNAPPY`                                                                                                                           | Parquet compression codec. See [available options](https://github.com/apache/parquet-format/blob/master/Compression.md).                                                                                                           |

### Compatibility with S3-compatible solutions

The S3 exporter also works with S3-compatible object stores such as **MinIO**, Ceph, or Garage: keep `kind: s3` and point the AWS SDK at your server with `AWS_ENDPOINT_URL_S3`.

> [!NOTE]
> Read the [**MinIO** page](minio.md) for the full setup, including bucket addressing behavior when using a hostname endpoint instead of an IP address.

## Configure the Go module

Add the following configuration to your `ffclient.Config{}` object:

> [!NOTE]
> - [**S3 Exporter v2**](https://pkg.go.dev/github.com/blackbirdworks/semaphore/exporter/s3exporterv2) uses [`aws-sdk-go-v2`](https://github.com/aws/aws-sdk-go-v2) to store evaluation data in S3.
> - [**S3 Exporter v1**](https://pkg.go.dev/github.com/blackbirdworks/semaphore/exporter/s3exporter) uses the deprecated [`aws-sdk-go`](https://github.com/aws/aws-sdk-go) v1 SDK.
> - [AWS ended support for AWS SDK for Go v1 on July 31, 2025](https://aws.amazon.com/blogs/developer/announcing-end-of-support-for-aws-sdk-for-go-v1-on-july-31-2025/), so migrate from S3 Exporter v1 to v2.

```go
awsConfig, _ := config.LoadDefaultConfig(context.Background())
ffConfig := ffclient.Config{
    // ...
    DataExporter: ffclient.DataExporter{
        // ...
        Exporter: &s3exporterv2.Exporter{
            Format:      "csv",
            Filename:    "flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}",
            CsvTemplate: "{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};{{ .Default}};{{ .Source}}\n",
            Bucket:      "my-bucket",
            S3Path:      "/semaphore/variations/",
            AwsConfig:   &awsConfig,
        },
    },
    // ...
}
client, err := ffclient.New(ffConfig)
defer client.Close()
```

| Field name                | Mandatory | Default                                                                                                                            | Description                                                                                                                                                                                    |
|---------------------------|:---------:|------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Bucket`                  | Yes       | **none**                                                                                                                           | Name of your S3 bucket.                                                                                                                                                                        |
| `AwsConfig`               | No        | `config.LoadDefaultConfig()`                                                                                                       | An instance of `*aws.Config` ([AWS SDK Go v2 docs](https://aws.github.io/aws-sdk-go-v2/docs/configuring-sdk/)). If unset, `config.LoadDefaultConfig()` reads standard AWS env vars and config. |
| `Format`                  | No        | `JSON`                                                                                                                             | Output file format: `JSON`, `CSV`, or `Parquet`.                                                                                                                                               |
| `Filename`                | No        | `flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}`                                                                       | Go template for exported filenames. Available fields: `{{ .Hostname}}`, `{{ .Timestamp}}`, and `{{ .Format}}`.                                                                                 |
| `CsvTemplate`             | No        | `{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};{{ .Default}};{{ .Source}}\n` | Go template for CSV rows (ignored for other formats). See [`exporter/feature_event.go`](https://github.com/BlackbirdWorks/semaphore/blob/main/exporter/feature_event.go) for available fields. |
| `S3Path`                  | No        | **bucket root level**                                                                                                              | Directory path inside the S3 bucket.                                                                                                                                                           |
| `ParquetCompressionCodec` | No        | `SNAPPY`                                                                                                                           | Parquet compression codec. See [available options](https://github.com/apache/parquet-format/blob/master/Compression.md).                                                                       |

`FlushInterval` and `MaxEventInMemory` are fields on `ffclient.DataExporter` (alongside `Exporter`), not on `s3exporterv2.Exporter`:

| Field              | Mandatory | Default    | Description                                                                                                 |
|--------------------|:---------:|------------|-------------------------------------------------------------------------------------------------------------|
| `FlushInterval`    | No        | `1 minute` | Interval (`time.Duration`) before flushing events (flushes earlier if `MaxEventInMemory` is reached first). |
| `MaxEventInMemory` | No        | `100000`   | Maximum number of events buffered in memory before flushing.                                                |
