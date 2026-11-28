# AWS S3

> [!NOTE]
> The relay proxy does not run retrievers: it reads its control plane from a store connection, see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). This page is about the retriever of the Go module.

## Overview

Load flag configurations from an AWS S3 bucket. Point to a single file with `item`, or omit `item` to load and merge multiple flag files from a bucket prefix.

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
