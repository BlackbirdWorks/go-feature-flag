# MinIO

## Overview

Export evaluation data to [MinIO](https://min.io), or any other S3-compatible object store such as Ceph or Garage. There is no dedicated MinIO exporter: use the [AWS S3 exporter](aws-s3.md) with `kind: s3` and point it at your server.

Every time `flushInterval` or `maxEventInMemory` is reached, a new file is added to your MinIO bucket.

## Configure the relay proxy

Use the `s3` exporter with an `s3` connection that points at your server. `pathStyle: true` addresses the bucket as
`{endpoint}/{bucket}`, so a hostname endpoint such as `http://minio:9000` works with no DNS setup on the MinIO side.

```yaml
version: 2
connections:
  evaluation-data:
    type: s3
    endpoint: http://minio:9000
    region: us-east-1
    bucket: evaluation-data-bucket
    pathStyle: true
    credentials:
      type: static
      accessKeyId: {fromEnv: MINIO_ACCESS_KEY}
      secretAccessKey: {fromEnv: MINIO_SECRET_KEY}
exporters:
  evaluations:
    kind: s3
    connection: evaluation-data
```

The other keys (`format`, `filename`, `path`, `csvTemplate`, `parquetCompressionCodec`) behave as on the [AWS S3 exporter page](aws-s3.md). The settings that every exporter takes (`flushInterval`, `maxEventInMemory`, `eventType`, `meta`, `overflowPolicy`, `retry` and the other pipeline settings) are in the [bootstrap file reference](../../relay-proxy/bootstrap-config.md) (section `exporters.<name>`).

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
