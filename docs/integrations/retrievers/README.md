# Store Your Flags (Retrievers)

## Overview

Semaphore loads your `YAML`, `JSON`, or `TOML` flag files from wherever you already store config using **retrievers**.

Every integration page below includes examples for both the **Relay Proxy** and the **Go Module**.

## Available retrievers

- [HTTP(S)](http.md): fetch flags from an HTTP(S) endpoint.
- [File System](file.md): load flags from a local file.
- [Kubernetes ConfigMap](kubernetes-configmap.md): load flags from a Kubernetes ConfigMap.
- [AWS S3](aws-s3.md): load flags from an AWS S3 bucket.
- [MinIO](minio.md): load flags from MinIO or any S3-compatible object store.
- [Google Cloud Storage](google-cloud-storage.md): load flags from a GCS bucket.
- [Azure Blob Storage](azure-blob-storage.md): load flags from Azure Blob Storage.
- [GitHub](github.md): load flags from a GitHub repository.
- [GitLab](gitlab.md): load flags from a GitLab repository.
- [Bitbucket](bitbucket.md): load flags from a Bitbucket repository.
- [MongoDB](mongodb.md): load flags from a MongoDB collection.
- [Redis](redis.md): load flags from Redis by key prefix.
- [PostgreSQL](postgresql.md): load flags from a PostgreSQL database.

## Custom retriever

Implement [`InitializableRetrieverWithFlagset`](https://pkg.go.dev/github.com/blackbirdworks/semaphore/retriever/#InitializableRetrieverWithFlagset) to load flags from a custom source:

```go
type InitializableRetrieverWithFlagset interface {
  Retrieve(ctx context.Context) ([]byte, error)
  Init(ctx context.Context, logger *slog.Logger, flagset *string) error
  Shutdown(ctx context.Context) error
  Status() retriever.Status
}
```

Semaphore calls `Retrieve` only when `Status()` returns `RetrieverReady`.

> [!NOTE]
> Want to contribute a retriever? Open a [pull request](https://github.com/BlackbirdWorks/semaphore/pulls).

> [!WARNING]
> Custom retrievers plug straight into the Go module, but require rebuilding the relay proxy binary to use them there.
