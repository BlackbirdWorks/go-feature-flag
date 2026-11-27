# MinIO

> [!NOTE]
> The relay proxy does not run retrievers: it reads its control plane from an S3 compatible store (`type: s3` with an
> `endpoint`), see below. The Go module is not affected.

## Overview

Load flag configurations from [MinIO](https://min.io) or any S3-compatible object store (such as Ceph or Garage). There is no dedicated MinIO retriever: use the [AWS S3 retriever](aws-s3.md) and point it at your server.

The same applies when [exporting evaluation data to MinIO](../exporters/minio.md).

## Configure the relay proxy

The relay proxy reads its control plane from an S3 compatible store through a `type: s3` connection of the
[bootstrap file](../../relay-proxy/bootstrap-config.md). Set `endpoint` to your server and `pathStyle: true`, which
addresses the bucket as `{endpoint}/{bucket}`, so a hostname endpoint works with no DNS setup on the MinIO side:

<!-- full-example -->
```yaml
version: 2
connections:
  control:
    type: s3
    endpoint: http://minio:9000
    region: us-east-1
    bucket: my-featureflag-bucket
    pathStyle: true
    credentials:
      type: static
      accessKeyId: {fromEnv: MINIO_ACCESS_KEY}
      secretAccessKey: {fromEnv: MINIO_SECRET_KEY}
controlPlane:
  connection: control
  manifest: {key: manifests/stable.yaml, channel: stable}
auth:
  adminKeys: [{fromEnv: SEMAPHORE_ADMIN_KEY}]
```

The relay serves nothing until the bucket exists and holds a control plane. Before it can serve flags:

1. Create the bucket, for example with the MinIO client: `mc alias set local http://localhost:9000 minioadmin minioadmin`
   then `mc mb local/my-featureflag-bucket`.
2. Publish the flags and the manifest with the same bootstrap file, with the secrets it names in the environment
   (`MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`): `semaphore-cli publish ./flags-repo --config bootstrap.yaml`. The source
   directory layout and the other flags are in [Publish](../../tooling/publish.md).

The relay then picks the manifest up at its next poll.

In Docker Compose, mount the file and pass it with `--config`:

```yaml
services:
  minio:
    image: minio/minio
    command: server /data --console-address ":9001"
    environment:
      MINIO_ROOT_USER: minioadmin
      MINIO_ROOT_PASSWORD: minioadmin
    ports:
      - "9000:9000"
      - "9001:9001"

  relay-proxy:
    image: ghcr.io/blackbirdworks/semaphore:latest
    command: ["--config", "/semaphore/bootstrap.yaml"]
    depends_on:
      - minio
    volumes:
      - ./bootstrap.yaml:/semaphore/bootstrap.yaml
    environment:
      MINIO_ACCESS_KEY: minioadmin
      MINIO_SECRET_KEY: minioadmin
      SEMAPHORE_ADMIN_KEY: change-me
    ports:
      - "1031:1031"
```

Other settings of the connection (`caBundleFile`, `requestChecksumCalculation`, `responseChecksumValidation`, timeouts)
are in the [bootstrap file reference](../../relay-proxy/bootstrap-config.md).

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

Your endpoint is a hostname, so the SDK uses virtual-host addressing and MinIO interprets the first path segment as the bucket name. Set `pathStyle: true` on the connection of the relay proxy, or `UsePathStyle` in the Go module.

### `resolve auth scheme: resolve endpoint: endpoint rule error, Invalid region`

The AWS SDK requires a region even for MinIO. Any value works: set `region` on the connection (the Go module reads `AWS_DEFAULT_REGION`).

### `WARN Response has no supported checksum. Not validating response payload.`

Harmless; set `responseChecksumValidation: whenRequired` on the connection (or `AWS_RESPONSE_CHECKSUM_VALIDATION=when_required` in the Go module) to silence it.
