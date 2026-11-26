# Retrieve your feature flags configuration

Provide a [retriever](https://pkg.go.dev/github.com/blackbirdworks/semaphore/retriever/#Retriever) in `ffclient.Config{}` during initialization to load your flag file.

## Available retrievers

- [HTTP(S)](../integrations/retrievers/http.md)
- [File System](../integrations/retrievers/file.md)
- [Kubernetes ConfigMap](../integrations/retrievers/kubernetes-configmap.md)
- [AWS S3](../integrations/retrievers/aws-s3.md)
- [MinIO](../integrations/retrievers/minio.md)
- [Google Cloud Storage](../integrations/retrievers/google-cloud-storage.md)
- [Azure Blob Storage](../integrations/retrievers/azure-blob-storage.md)
- [GitHub](../integrations/retrievers/github.md)
- [GitLab](../integrations/retrievers/gitlab.md)
- [Bitbucket](../integrations/retrievers/bitbucket.md)
- [MongoDB](../integrations/retrievers/mongodb.md)
- [Redis](../integrations/retrievers/redis.md)
- [PostgreSQL](../integrations/retrievers/postgresql.md)

> [!NOTE]
> Each retriever page documents its configuration options.

## Explicitly call the retrievers

By default, retrievers refresh flags on `PollingInterval`. To force an immediate refresh (for example, in a CI pipeline or after updating a flag file), call `ForceRefresh` on the client:

```go
// Init ffclient with a file retriever.
client, _ := ffclient.New(&ffclient.Config{
    PollingInterval: 10 * time.Minute,
    Retriever: &fileretriever.Retriever{
        Path: "xxxx.yaml",
    },
})

// ...
client.ForceRefresh()
// ...
```
