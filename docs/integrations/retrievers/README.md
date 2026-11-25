# Store your flags’ configuration
## Overview
Semaphore is a tool that makes it easy to implement feature flags in your application.

One of the benefits of using Semaphore is that it is designed to be simple and lightweight.  
To achieve this, the solution offers flexible ways to manage and store your feature flag configurations.

The goal is to let you store your flag configuration where is suits you the best, and to do this we have built a list
of retrievers that are able to load your file from different sources.

## Where to store the flag's configuration

The easiest way to get started with Semaphore is to store your flags' configuration in a file.

You can then upload those files where you want, and Semaphore will use it.
The way the solution achieves this is through the use of retrievers, which allow you to load your feature flag
configuration file from various sources.

**Semaphore** supports a variety of retrievers out of the box _(see list below)_.

You have 2 different ways to specify a retriever depending on if you are using the **GO Module** or the **Relay Proxy**.
In all the detail pages of the retrievers below you will have an example for both usages (relay proxy and Go module).

## Available retrievers

Bellow is the full list of retrievers that are available in Semaphore.

- [HTTP(S)](http.md): Fetches the configuration from a remote URL over HTTP(S).
- [File System](file.md): Fetch the configuration from a local file.
- [Kubernetes ConfigMap](kubernetes-configmap.md): Loads the configuration from a Kubernetes ConfigMap.
- [AWS S3](aws-s3.md): Retrieves the configuration from an AWS S3 bucket.
- [MinIO](minio.md): Retrieves the configuration from MinIO, or any other S3-compatible object store.
- [Google Cloud Storage](google-cloud-storage.md): Retrieves the configuration from a Google Cloud Storage bucket.
- [Azure Blob Storage](azure-blob-storage.md): Retrieves the configuration from an Azure Blob Storage.
- [GitHub](github.md): Fetch the configuration from files stored in a GIT repository.
- [GitLab](gitlab.md): Fetch the configuration from files stored in a GIT repository.
- [Bitbucket](bitbucket.md): Fetch the configuration from files stored in a GIT repository.
- [MongoDB](mongodb.md): Load the configuration from a MongoDB collection.
- [Redis](redis.md): Load the configuration from Redis using a specific prefix.
- [Postgresql](postgresql.md): Load the configuration from Postgresql database.

## Custom retriever
If you have a specific use case that is not covered by the built-in retrievers, you can create your own custom retriever.

To achieve this, you need to implement the [`InitializableRetrieverWithFlagset`](https://pkg.go.dev/github.com/blackbirdworks/semaphore/retriever/#InitializableRetrieverWithFlagset) interface in your custom retriever.

```go
type InitializableRetrieverWithFlagset interface {
  Retrieve(ctx context.Context) ([]byte, error)
  Init(ctx context.Context, logger *slog.Logger, flagset *string) error
  Shutdown(ctx context.Context) error
  Status() retriever.Status
}
```

To avoid any issue to call the Retrieve function before the Init function, you have to manage the status of your retriever.
Semaphore will try to call the Retrieve function only if the status is `RetrieverReady`.

> [!NOTE]
> If you think your custom retriever could be useful for others, feel free to open a [pull request](https://github.com/BlackbirdWorks/semaphore/pulls) to add it to the list of available retrievers.

> [!WARNING]
> If you are using a custom retriever, it is easy to use it for the GO Module, but if you want to use it in the relay proxy you will have to recompile it yourself.
