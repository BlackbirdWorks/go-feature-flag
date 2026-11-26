# Google Cloud Storage

## Overview

Load flag configurations from a Google Cloud Storage bucket.

The [**Google Cloud Storage Retriever**](https://pkg.go.dev/github.com/blackbirdworks/semaphore/retriever/gcstorageretriever/#Retriever) uses the [`cloud.google.com/go/storage`](https://pkg.go.dev/cloud.google.com/go/storage) and [`google.golang.org/api/option`](https://pkg.go.dev/google.golang.org/api/option) packages to access your flags in Google Cloud Storage.

## Configure the relay proxy

Add this configuration to your relay proxy config file:

```yaml
# ...
retrievers:
  - kind: googleStorage
    bucket: 2093u4pkasjc3
    object: flags.yaml
# ...
```

| Field name | Mandatory | Type   | Default  | Description |
|------------|:---------:|--------|----------|-------------|
| `kind`     | Yes       | string | **none** | Value should be **`googleStorage`**.<br/>_Mandatory field identifying the retriever._ |
| `bucket`   | Yes       | string | **none** | Name of your Google Cloud Storage bucket _(ex: `my-featureflag-bucket`)_. |
| `object`   | Yes       | string | **none** | Path to the file inside the bucket _(ex: `config/flag/my-flags.yaml`)_. |

> [!NOTE]
> Use GCP native authentication or set `GOOGLE_APPLICATION_CREDENTIALS` to authenticate with Google Cloud. See the [GCP authentication docs](https://cloud.google.com/docs/authentication) for details.

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
