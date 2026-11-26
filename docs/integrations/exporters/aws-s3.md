# AWS S3

## Overview
The S3 exporter will collect the data and create a new file in a specific folder everytime we send the data.

Everytime the `FlushInterval` or `MaxEventInMemory` is reached a new file will be added to AWS S3.

> [!NOTE]
> If for some reason the AWS S3 upload fails, we will keep the data in memory and retry to add the next time we reach `FlushInterval` or `MaxEventInMemory`.

## Configure the relay proxy

To configure your relay proxy to use the AWS S3 exporter, you need to add the following
configuration to your relay proxy configuration file:

```yaml
# ...
exporters:
  - kind: s3
    bucket: evaluation-data-bucket
# ...
```

> [!NOTE]
> If you are using the S3 exporter, the easiest way to provide credentials is to set environment variables. It will be used by Semaphore to identify to your S3 bucket.
>
> ```shell
> export AWS_SECRET_ACCESS_KEY=xxxx
> export AWS_ACCESS_KEY_ID=xxx
> export AWS_DEFAULT_REGION=eu-west-1
> ```

| Field name                |    Mandatory     | Type   | Default                                                                                                                            | Description                                                                                                                                                                                                                                                                                                                                                                           |
|---------------------------|:----------------:|--------|------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `kind`                    |  Yes   | string | **none**                                                                                                                           | **(mandatory)** Value should be **`s3`**.<br/>_This field is mandatory and describes which retriever you are using._                                                                                                                                                                                                                                                                  |
| `bucket`                  |  Yes   | string | **none**                                                                                                                           | **(mandatory)** Name of your S3 Bucket.                                                                                                                                                                                                                                                                                                                                               |
| `flushInterval`           | No | int    | `60000`                                                                                                                            | The interval in millisecond between 2 calls to the webhook _(if the `maxEventInMemory` is reached before the flushInterval we will call the exporter before)_.                                                                                                                                                                                                                        |
| `maxEventInMemory`        | No | int    | `100000`                                                                                                                           | If we hit that limit we will call the exporter.                                                                                                                                                                                                                                                                                                                                       |
| `format`                  | No | string | `JSON`                                                                                                                             | Format is the output format you want in your exported file. Available format: `JSON`, `CSV`, `Parquet`.                                                                                                                                                                                                                                                                               |
| `filename`                | No | string | `flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}`                                                                       | You can use a config template to define the name of your exported files. Available replacements are `{{ .Hostname}}`, `{{ .Timestamp}}` and `{{ .Format}}`.                                                                                                                                                                                                                           |
| `csvTemplate`             | No | string | `{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};{{ .Default}};{{ .Source}}\n` | CsvTemplate is used if your output format is CSV.<br/>This field will be ignored if you are using format other than CSV.<br/>You can decide which fields you want in your CSV line with a go-template syntax, please check [`exporter/feature_event.go`](https://github.com/BlackbirdWorks/semaphore/blob/main/exporter/feature_event.go) to see what are the fields available. |
| `path`                    | No | string | **bucket root level**                                                                                                              | The location of the directory in S3.                                                                                                                                                                                                                                                                                                                                                  |
| `parquetCompressionCodec` | No | string | `SNAPPY`                                                                                                                           | ParquetCompressionCodec is the parquet compression codec for better space efficiency. [Available options](https://github.com/apache/parquet-format/blob/master/Compression.md)                                                                                                                                                                                                        |

### Compatibility with S3-Compatible Solutions

The S3 exporter also works with S3-compatible object stores such as **MinIO**, Ceph or Garage: keep `kind: s3` and
point the AWS SDK at your server with `AWS_ENDPOINT_URL_S3`.

> [!NOTE]
> Read the [**MinIO** page](minio.md) for the full setup, including the bucket addressing gotcha you will hit if your
> endpoint is a hostname rather than an IP address.

## Configure the Go Module
To configure your Go module to use the AWS S3 exporter, you need to add the following
configuration to your `ffclient.Config{}` object:

> [!NOTE]
> The [**S3 Exporter v2**](https://pkg.go.dev/github.com/blackbirdworks/semaphore/exporter/s3exporterv2) will use the [`aws-sdk-go-v2`](https://github.com/aws/aws-sdk-go-v2) to store your evaluation data in an S3 bucket.
>
> The [**S3 Exporter v1**](https://pkg.go.dev/github.com/blackbirdworks/semaphore/exporter/s3exporter) uses the deprecated [`aws-sdk-go`](https://github.com/aws/aws-sdk-go) to write evaluation data to S3.
>
> [AWS ended support for AWS SDK for Go v1 on July 31, 2025](https://aws.amazon.com/blogs/developer/announcing-end-of-support-for-aws-sdk-for-go-v1-on-july-31-2025/), so migrate from S3 Exporter v1 to v2.

```go
awsConfig, _ := config.LoadDefaultConfig(context.Background())
config := ffclient.Config{
    // ...
   DataExporter: ffclient.DataExporter{
        // ...
        Exporter: &s3exporterv2.Exporter{
            Format: "csv",
            FileName: "flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}",
            CsvTemplate: "{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};{{ .Default}};{{ .Source}}\n",
            Bucket:    "my-bucket",
            S3Path:    "/go-feature-flag/variations/",
            Filename:  "flag-variation-{{ .Timestamp}}.{{ .Format}}",
            AwsConfig: &awsConfig,
        },
    },
    // ...
}
client, err := ffclient.New(config)
defer client.Close()
```

| Field name                |    Mandatory     | Default                                                                                                                            | Description                                                                                                                                                                                                                                                                                                                                                                           |
|---------------------------|:----------------:|------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Bucket`                  |  Yes   | **none**                                                                                                                           | Name of your S3 Bucket.                                                                                                                                                                                                                                                                                                                                                               |
| `AwsConfig`               | No | `config.LoadDefaultConfig()`                                                                                                       | An instance of `aws.Config` that configures your access to AWS *(see [this documentation for more info](https://aws.github.io/aws-sdk-go-v2/docs/configuring-sdk/))*.<br/>If not set, `config.LoadDefaultConfig()` is called for you, which reads the standard AWS environment variables and shared config files.                                                                       |
| `FlushInterval`           | No | `60000`                                                                                                                            | The interval in millisecond between 2 calls to the webhook _(if the `maxEventInMemory` is reached before the flushInterval we will call the exporter before)_.                                                                                                                                                                                                                        |
| `MaxEventInMemory`        | No | `100000`                                                                                                                           | If we hit that limit we will call the exporter.                                                                                                                                                                                                                                                                                                                                       |
| `Format`                  | No | `JSON`                                                                                                                             | Format is the output format you want in your exported file. Available format: `JSON`, `CSV`, `Parquet`.                                                                                                                                                                                                                                                                               |
| `Filename`                | No | `flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}`                                                                       | You can use a config template to define the name of your exported files. Available replacements are `{{ .Hostname}}`, `{{ .Timestamp}}` and `{{ .Format}}`.                                                                                                                                                                                                                           |
| `CsvTemplate`             | No | `{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};{{ .Default}};{{ .Source}}\n` | CsvTemplate is used if your output format is CSV.<br/>This field will be ignored if you are using format other than CSV.<br/>You can decide which fields you want in your CSV line with a go-template syntax, please check [`exporter/feature_event.go`](https://github.com/BlackbirdWorks/semaphore/blob/main/exporter/feature_event.go) to see what are the fields available. |
| `Path`                    | No | **bucket root level**                                                                                                              | The location of the directory in S3.                                                                                                                                                                                                                                                                                                                                                  |
| `ParquetCompressionCodec` | No | `SNAPPY`                                                                                                                           | ParquetCompressionCodec is the parquet compression codec for better space efficiency. [Available options](https://github.com/apache/parquet-format/blob/master/Compression.md)                                                                                                                                                                                                        |
