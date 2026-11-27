# MinIO

> [!NOTE]
> The relay proxy does not run retrievers any more: it reads a control plane store (S3 or a directory), see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). The relay configuration examples on this page are version 1 and
> are not read by the relay; the retriever kinds come back to the relay with the control plane connections (issue #57).
> The Go module is not affected.

## Overview

Load flag configurations from [MinIO](https://min.io) or any S3-compatible object store (such as Ceph or Garage). There is no dedicated MinIO retriever: use the [AWS S3 retriever](aws-s3.md) with `kind: s3` and point it at your server.

The same applies when [exporting evaluation data to MinIO](../exporters/minio.md).

## Configure the relay proxy

Configure `kind: s3` and point the AWS SDK at MinIO using environment variables:

```yaml
# ...
retrievers:
  - kind: s3
    bucket: my-featureflag-bucket
    item: flag/flags.yaml
# ...
```

```shell
export AWS_ENDPOINT_URL_S3=http://127.0.0.1:9000
export AWS_ACCESS_KEY_ID=minioadmin
export AWS_SECRET_ACCESS_KEY=minioadmin
export AWS_DEFAULT_REGION=us-east-1
```

| Field name                       | Mandatory | Type                 | Default  | Description |
|----------------------------------|:---------:|----------------------|----------|-------------|
| `kind`                           | Yes       | string               | **none** | **Value should be `s3`.**<br/>_There is no `minio` kind; MinIO uses the S3 retriever._ |
| `bucket`                         | Yes       | string               | **none** | Name of your MinIO bucket _(ex: `my-featureflag-bucket`)_. |
| `item`                           | No        | string               | **none** | Path to the file inside the bucket _(ex: `config/flag/my-flags.yaml`)_.<br/>_Omit to load a folder; see the [AWS S3 page](aws-s3.md#load-several-files-from-a-folder)._ |
| `prefix`, `pattern`, `recursive` | No        | string, string, bool | **none** | Folder mode filters, only usable without `item`. `pattern` matches the file name only _(no `/`)_, and files with unsupported extensions are skipped with a warning. See the [AWS S3 page](aws-s3.md#load-several-files-from-a-folder). |

> [!WARNING]
> **Use an IP address in `AWS_ENDPOINT_URL_S3`, or configure virtual-host DNS below**
> The AWS SDK addresses buckets as `http://{bucket}.{your-endpoint}` _(virtual-host style)_, **except** when your endpoint is an IP address. `http://127.0.0.1:9000` works out of the box, but `http://minio:9000` makes the relay proxy call `http://my-featureflag-bucket.minio:9000` and fail.
>
> The relay proxy has no setting to force path-style addressing, so hostname endpoints require virtual-host resolution — see [Using a hostname endpoint](#using-a-hostname-endpoint).

### Using a hostname endpoint

If you cannot use an IP address _(Docker Compose, Kubernetes, …)_, set `MINIO_DOMAIN` on MinIO **and** make `{bucket}.{host}` resolve to MinIO.

In Docker Compose, add `MINIO_DOMAIN` and one network alias per bucket:

```yaml
services:
  minio:
    image: minio/minio
    command: server /data --console-address ":9001"
    environment:
      MINIO_ROOT_USER: minioadmin
      MINIO_ROOT_PASSWORD: minioadmin
      MINIO_DOMAIN: minio
    networks:
      default:
        aliases:
          - my-featureflag-bucket.minio
    ports:
      - "9000:9000"
      - "9001:9001"

  relay-proxy:
    image: ghcr.io/blackbirdworks/semaphore:latest
    depends_on:
      - minio
    volumes:
      - ./semaphore-proxy.yaml:/semaphore/semaphore-proxy.yaml
    environment:
      AWS_ENDPOINT_URL_S3: http://minio:9000
      AWS_ACCESS_KEY_ID: minioadmin
      AWS_SECRET_ACCESS_KEY: minioadmin
      AWS_DEFAULT_REGION: us-east-1
    ports:
      - "1031:1031"
```

> [!NOTE]
> The network alias is required because Docker DNS does not resolve wildcards: without `my-featureflag-bucket.minio`, the relay proxy cannot reach the bucket even with `MINIO_DOMAIN` set.
>
> In the **Go module**, you can avoid this by using `S3ClientOptions` to force path-style addressing _(see below)_.

## Configure the Go module

The Go module exposes `S3ClientOptions` to force path-style addressing. This works with any hostname or IP endpoint without MinIO-side DNS setup:

```go
awsConfig, _ := config.LoadDefaultConfig(context.Background(),
    config.WithBaseEndpoint("http://minio:9000"))

client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &s3retrieverv2.Retriever{
        Bucket:    "my-featureflag-bucket",
        Item:      "flag/flags.yaml",
        AwsConfig: &awsConfig,
        S3ClientOptions: []func(*s3.Options){
            func(o *s3.Options) { o.UsePathStyle = true },
        },
    },
})
defer client.Close()
```

| Field                 | Mandatory | Description |
|-----------------------|:---------:|-------------|
| **`Bucket`**          | Yes       | Name of your bucket. |
| **`Item`**            | No        | Location of your file in the bucket. |
| **`AwsConfig`**       | No        | `aws.Config` instance. Set your MinIO endpoint with `config.WithBaseEndpoint()`. If omitted, `config.LoadDefaultConfig()` reads `AWS_ENDPOINT_URL_S3` and standard AWS environment variables. |
| **`S3ClientOptions`** | No        | Functional options passed to the S3 client. Set `o.UsePathStyle = true` to address buckets as `{endpoint}/{bucket}`. |

> [!NOTE]
> `AwsConfig` is optional. If omitted, Semaphore calls `config.LoadDefaultConfig()` using `AWS_ENDPOINT_URL_S3`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and `AWS_DEFAULT_REGION`, so you only need `S3ClientOptions` to force path-style addressing:
>
> ```go
> Retriever: &s3retrieverv2.Retriever{
>     Bucket: "my-featureflag-bucket",
>     Item:   "flag/flags.yaml",
>     S3ClientOptions: []func(*s3.Options){
>         func(o *s3.Options) { o.UsePathStyle = true },
>     },
> },
> ```

## Troubleshooting

### `NoSuchBucket: The specified bucket does not exist` — but the bucket exists

Your endpoint is a hostname, so the SDK uses virtual-host addressing and MinIO interprets the first path segment as the bucket name. Use an IP endpoint, configure [`MINIO_DOMAIN` and a DNS alias](#using-a-hostname-endpoint), or set `UsePathStyle` in the Go module.

### `dial tcp ...: no route to host` on `http://my-featureflag-bucket.minio:9000`

Same cause, except the virtual-host name does not resolve at all. The bucket name prepended to the URL is the indicator.

### `resolve auth scheme: resolve endpoint: endpoint rule error, Invalid region`

The AWS SDK requires a region even for MinIO. Any value works:

```shell
export AWS_DEFAULT_REGION=us-east-1
```

### `WARN Response has no supported checksum. Not validating response payload.`

Harmless; silence it if it clutters your logs:

```shell
export AWS_RESPONSE_CHECKSUM_VALIDATION=when_required
```
