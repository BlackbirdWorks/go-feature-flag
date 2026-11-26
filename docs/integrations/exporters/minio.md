# MinIO

## Overview

Export evaluation data to [MinIO](https://min.io), or any other S3-compatible object store such as Ceph or Garage. There is no dedicated MinIO exporter: use the [AWS S3 exporter](aws-s3.md) with `kind: s3` and point it at your server.

Every time `flushInterval` or `maxEventInMemory` is reached, a new file is added to your MinIO bucket.

## Configure the relay proxy

Use the standard `s3` exporter configuration and select MinIO with AWS SDK environment variables:

```yaml
# ...
exporters:
  - kind: s3
    bucket: evaluation-data-bucket
    path: /semaphore/variations/
# ...
```

```shell
export AWS_ENDPOINT_URL_S3=http://127.0.0.1:9000
export AWS_ACCESS_KEY_ID=minioadmin
export AWS_SECRET_ACCESS_KEY=minioadmin
export AWS_DEFAULT_REGION=us-east-1
```

Only `kind` and `bucket` are mandatory. All other fields (`format`, `filename`, `path`, `flushInterval`, `maxEventInMemory`, `csvTemplate`, `parquetCompressionCodec`) behave as documented on the [AWS S3 exporter page](aws-s3.md).

> [!WARNING]
> **Use an IP address in `AWS_ENDPOINT_URL_S3`, or read below**
> The AWS SDK addresses buckets as `http://{bucket}.{your-endpoint}` (virtual-host style), **except** when your endpoint is an IP address. `http://127.0.0.1:9000` works out of the box, whereas `http://minio:9000` makes the relay proxy call `http://evaluation-data-bucket.minio:9000` and fail.
>
> The relay proxy has no setting to force path-style addressing. If you cannot use an IP address, set `MINIO_DOMAIN` on your MinIO server and make `{bucket}.{host}` resolve to it (see the [MinIO retriever page](../retrievers/minio.md#using-a-hostname-endpoint) for a full Docker Compose example).

## Configure the Go module

The Go module exposes `S3ClientOptions` to force path-style addressing. This works with any hostname or IP endpoint without MinIO-side configuration:

```go
awsConfig, _ := config.LoadDefaultConfig(context.Background(),
    config.WithBaseEndpoint("http://minio:9000"))

ffConfig := ffclient.Config{
    // ...
    DataExporter: ffclient.DataExporter{
        // ...
        Exporter: &s3exporterv2.Exporter{
            Format:    "json",
            Bucket:    "evaluation-data-bucket",
            S3Path:    "/semaphore/variations/",
            Filename:  "flag-variation-{{ .Timestamp}}.{{ .Format}}",
            AwsConfig: &awsConfig,
            S3ClientOptions: []func(*s3.Options){
                func(o *s3.Options) { o.UsePathStyle = true },
            },
        },
    },
    // ...
}
client, err := ffclient.New(ffConfig)
defer client.Close()
```

| Field             | Mandatory | Description                                                                                                                                                                                                 |
|-------------------|:---------:|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Bucket`          | Yes       | Name of your bucket.                                                                                                                                                                                        |
| `AwsConfig`       | No        | An instance of `*aws.Config`. Set your MinIO endpoint with `config.WithBaseEndpoint()`. If omitted, `config.LoadDefaultConfig()` reads `AWS_ENDPOINT_URL_S3` and the standard AWS environment variables.    |
| `S3ClientOptions` | No        | Functional options passed to the S3 client. Set `o.UsePathStyle = true` to address buckets as `{endpoint}/{bucket}`, which MinIO expects by default.                                                        |

> [!NOTE]
> `AwsConfig` is optional. If omitted, Semaphore calls `config.LoadDefaultConfig()` using `AWS_ENDPOINT_URL_S3`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and `AWS_DEFAULT_REGION` from your environment. You only need `S3ClientOptions` to force path-style addressing.

Remaining fields are shared with the [AWS S3 exporter](aws-s3.md#configure-the-go-module).

## Troubleshooting

### `NoSuchBucket: The specified bucket does not exist` — but the bucket does exist

Your endpoint is a hostname, so the SDK uses virtual-host addressing and MinIO reads the first path segment as the bucket name. Use an IP endpoint, configure [`MINIO_DOMAIN` and a DNS alias](../retrievers/minio.md#using-a-hostname-endpoint), or set `UsePathStyle` in the Go module.

### `resolve auth scheme: resolve endpoint: endpoint rule error, Invalid region`

The AWS SDK requires a region even when talking to MinIO. Any value works:

```shell
export AWS_DEFAULT_REGION=us-east-1
```

### `WARN Response has no supported checksum. Not validating response payload.`

Harmless, and can be silenced with:

```shell
export AWS_RESPONSE_CHECKSUM_VALIDATION=when_required
```
