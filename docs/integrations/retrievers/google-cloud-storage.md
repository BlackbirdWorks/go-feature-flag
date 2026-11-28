# Google Cloud Storage

> [!NOTE]
> The relay proxy does not run retrievers: it reads its control plane from a store connection, see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). This page is about the retriever of the Go module.

## Overview

Load flag configurations from a Google Cloud Storage bucket.

The [**Google Cloud Storage Retriever**](https://pkg.go.dev/github.com/blackbirdworks/semaphore/retriever/gcstorageretriever/#Retriever) uses the [`cloud.google.com/go/storage`](https://pkg.go.dev/cloud.google.com/go/storage) and [`google.golang.org/api/option`](https://pkg.go.dev/google.golang.org/api/option) packages to access your flags in Google Cloud Storage.

## Configure the Go module

Add the retriever to `ffclient.Config{}`:

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &gcstorageretriever.Retriever{
        Options: []option.ClientOption{option.WithoutAuthentication()},
        Bucket:  "2093u4pkasjc3",
        Object:  "flags.yaml",
    },
})
defer client.Close()
```

| Field         | Mandatory | Description |
|---------------|:---------:|-------------|
| **`Bucket`**  | Yes       | Name of your bucket. |
| **`Object`**  | Yes       | Name of your object in the bucket. |
| **`Options`** | No        | Slice of `option.ClientOption` (`[]option.ClientOption`) configuring Google Cloud access. See the [GCP authentication docs](https://cloud.google.com/docs/authentication). |
