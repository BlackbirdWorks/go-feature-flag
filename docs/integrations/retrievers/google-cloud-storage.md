# Google Cloud Storage

## Overview
Retrieves the configuration from a Google Cloud Storage bucket. This retriever is useful when you are using Google Cloud and want to use GCS to store your configuration files.

The [**Google Cloud Storage Retriever**](https://pkg.go.dev/github.com/blackbirdworks/semaphore/retriever/gcstorageretriever/#Retriever) will use the [`google-cloud-storage package`](https://pkg.go.dev/cloud.google.com/go/storage) and [`google-api-options package`](https://pkg.go.dev/google.golang.org/api/option) to access your flag in Google Cloud Storage.

## Configure the relay proxy

To configure your relay proxy to use the Google Cloud Storage retriever, you need to add the following
configuration to your relay proxy configuration file:

```yaml
# ...
retrievers:
  - kind: googleStorage
    bucket: 2093u4pkasjc3
    object: flags.yaml
# ...
```

| Field name |   Mandatory   | Type   | Default  | Description                                                                                                     |
|------------|:-------------:|--------|----------|-----------------------------------------------------------------------------------------------------------------|
| `kind`     | Yes | string | **none** | Value should be **`googleStorage`**.<br/>_This field is mandatory and describes which retriever you are using._ |
| `bucket`   | Yes | string | **none** | This is the name of your Google Storage bucket _(ex: `my-featureflag-bucket`)_.                                 |
| `object`   | Yes | string | **none** | Path to the file inside the bucket _(ex: `config/flag/my-flags.yaml`)_.                                         |

> [!NOTE]
> If you want to authenticate with Google Cloud, you can use the default environment variable `GOOGLE_APPLICATION_CREDENTIALS` to provide your credentials.
> We recommend using GCP native authentication methods.
>
> Check [this documentation for more info](https://cloud.google.com/docs/authentication).

## Configure the Go Module
To configure your Go module to use the Google Cloud Storage retriever, you need to add the following
configuration to your `ffclient.Config{}` object:

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &gcstorageretriever.Retriever{
	    Options: []option.ClientOption{option.WithoutAuthentication()},
		Bucket: "2093u4pkasjc3",
		Object: "flags.yaml",
	}
})
defer client.Close()
```

| Field        |    Mandatory     | Description                                                                                                                                                                     |
|--------------|:----------------:|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **`Bucket`** |  Yes   | The name of your bucket.                                                                                                                                                        |
| **`Object`** |  Yes   | The name of your object in your bucket.                                                                                                                                         |
| **`Option`** | No | An instance of `option.ClientOption` that configures your access to Google Cloud. <br/> Check [this documentation for more info](https://cloud.google.com/docs/authentication). |
