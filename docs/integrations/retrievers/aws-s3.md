# AWS S3

> [!NOTE]
> The relay proxy does not run retrievers any more: it reads a control plane store (S3 or a directory), see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). The relay configuration examples on this page are version 1 and
> are not read by the relay; the retriever kinds come back to the relay with the control plane connections (issue #57).
> The Go module is not affected.

## Overview

Load flag configurations from an AWS S3 bucket. Point to a single file with `item`, or omit `item` to load and merge multiple flag files from a bucket prefix.

## Configure the relay proxy

Add this configuration to your relay proxy config file:

```yaml
# ...
retrievers:
  - kind: s3
    bucket: my-featureflag-bucket
    item: flag/flags.yaml
# ...
```

> [!NOTE]
> Set AWS credentials using standard environment variables so Semaphore can authenticate to your S3 bucket:
>
> ```shell
> export AWS_SECRET_ACCESS_KEY=xxxx
> export AWS_ACCESS_KEY_ID=xxx
> export AWS_DEFAULT_REGION=eu-west-1
> ```

| Field name  | Mandatory | Type   | Default  | Description |
|-------------|:---------:|--------|----------|-------------|
| `kind`      | Yes       | string | **none** | **Value should be `s3`.**<br/>_Mandatory field identifying the retriever._ |
| `bucket`    | Yes       | string | **none** | Name of your S3 bucket _(ex: `my-featureflag-bucket`)_. |
| `item`      | No        | string | **none** | Path to the file inside the bucket _(ex: `config/flag/my-flags.yaml`)_.<br/>_Set to load one file; omit to load a folder (see [Load several files from a folder](#load-several-files-from-a-folder))._ |
| `prefix`    | No        | string | **none** | _Folder mode only._ Key prefix _(ex: `flags/`)_ restricting loaded files. Empty or `/` targets the bucket root. |
| `pattern`   | No        | string | **none** | _Folder mode only._ Optional glob matched against the file name only _(ex: `*.yaml`)_; cannot contain `/`. If empty, every file is used. |
| `recursive` | No        | bool   | `false`  | _Folder mode only._ If `true`, files in nested "folders" are also loaded; otherwise only direct children of `prefix` are loaded. |

**Set `item` to load a single file, or omit it to load a folder.** `prefix`, `pattern`, and `recursive` cannot be used with `item`.

### Load several files from a folder

When `item` is omitted, the retriever lists objects in the bucket _(filtered by `prefix`, `pattern`, and `recursive`)_, downloads them, and merges their flags into a single flag set. This lets multiple teams publish separate flag files to the same bucket.

```yaml
retrievers:
  - kind: s3
    bucket: my-featureflag-bucket
    prefix: flags/
    pattern: "*.yaml"
    recursive: true
```

> [!WARNING]
> Without `item` **and** without `prefix`, the **root of the bucket** is read _(everything below it when `recursive: true`)_. Set `pattern` when other files live there. Also beware that a typo in the `item` key silently switches the retriever to folder mode.

How it works:
- **Format**: derived from the file extension _(`.json`, `.yaml`/`.yml`, `.toml`)_. Every matched file must use the same format, or retrieval fails with an error naming the offending files. Use `pattern` to select a single format when a prefix contains multiple formats. Files with unsupported extensions are skipped with a log warning _(logged once while the file remains)_.
- **Merge**: top-level flag keys from every file are merged. Duplicate flag keys across two files fail retrieval with an error naming the flag and both files.
- **Polling**: lists objects on each poll and only downloads new files or files whose `ETag` changed. Objects without an `ETag` are downloaded on every poll; unchanged objects are served from memory.
- **Deletion**: removing a file from the bucket removes its flags on the next poll. Matching zero files produces an empty flag set _(logs a warning, not an error)_.
- Keys ending with `/` _(folder markers)_ are ignored. Without `recursive`, only direct children of `prefix` are used.
- Without `prefix` _(or with `prefix: /`)_, the bucket root is used: top-level files only, or the whole bucket with `recursive: true`. Leading `/` characters are trimmed _(`/flags/` becomes `flags/`)_ because S3 keys never start with `/`.
- A missing trailing `/` is added to `prefix`, so `flags` and `flags/` are equivalent and `flags-old/...` is never matched.
- Empty files are accepted in any format and contribute no flags.
- Each file is parsed like a single `item` file, so any valid `item` file is valid in folder mode.

> [!NOTE]
> **IAM permissions**: In folder mode, Semaphore needs `s3:ListBucket` on the bucket _(`arn:aws:s3:::my-featureflag-bucket`)_ in addition to `s3:GetObject` on the objects _(`arn:aws:s3:::my-featureflag-bucket/flags/*`)_.

### Compatibility with S3-compatible solutions

The S3 retriever works with S3-compatible stores such as **MinIO**, Ceph, and Garage: keep `kind: s3` and set `AWS_ENDPOINT_URL_S3` to your server URL.

> [!NOTE]
> See the [**MinIO** page](minio.md) for the full setup, including the bucket addressing gotcha when using a hostname instead of an IP address.

## Configure the Go module

Add the retriever to `ffclient.Config{}` using [`s3retrieverv2`](https://pkg.go.dev/github.com/blackbirdworks/semaphore/retriever/s3retrieverv2) (`aws-sdk-go-v2`):

```go
awsConfig, _ := config.LoadDefaultConfig(context.Background())
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &s3retrieverv2.Retriever{
        Bucket:    "my-featureflag-bucket",
        Item:      "flag/flags.yaml",
        AwsConfig: &awsConfig,
    },
})
defer client.Close()
```

To load several files from a folder _(omit `Item`)_:

```go
Retriever: &s3retrieverv2.Retriever{
    Bucket:    "my-featureflag-bucket",
    Prefix:    "flags/",
    Pattern:   "*.yaml",
    Recursive: true,
    AwsConfig: &awsConfig,
},
```

| Field           | Mandatory | Description |
|-----------------|:---------:|-------------|
| **`Bucket`**    | Yes       | Name of your S3 bucket. |
| **`Item`**      | No        | Location of your file in the bucket. If omitted, the retriever runs in folder mode and merges files (see [above](#load-several-files-from-a-folder)). |
| **`Prefix`**    | No        | _Folder mode only._ Key prefix _(ex: `flags/`)_. Empty or `/` targets the bucket root. Requires `s3:ListBucket`. |
| **`Pattern`**   | No        | _Folder mode only._ Glob matched against the file name only; cannot contain `/` _(ex: `*.yaml`)_. |
| **`Recursive`** | No        | _Folder mode only._ If `true`, files in nested "folders" are also loaded. Defaults to `false`. |
| **`AwsConfig`** | No        | `aws.Config` instance configuring AWS access (see the [AWS SDK for Go v2 docs](https://aws.github.io/aws-sdk-go-v2/docs/configuring-sdk/)). If omitted, `config.LoadDefaultConfig()` is called automatically using standard AWS environment variables and shared config files. |
