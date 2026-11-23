# Retrieve your feature flags configuration

The module supports different ways of retrieving the flag file.

To retrieve a file you need to provide a [retriever](https://pkg.go.dev/github.com/blackbirdworks/semaphore/retriever/#Retriever) in your `ffclient.Config{}` during the initialization.  

**Available retrievers**:

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
- [Postgresql](../integrations/retrievers/postgresql.md)

> [!NOTE]
> In each retriever documentation, you will find the configuration needed to set up the retriever.

## Explicitly call the retrievers

By default, the retrievers are called regularly to refresh the configuration based on the polling interval.

But there are use cases where you want to refresh the configuration explicitly _(for example, during the CI process
after you have changed your configuration file)_.

To do that, you can call the `ForceRefresh` method on the client.

```go
// Init ffclient with a file retriever.
	goff, _ := ffclient.New(ffclient.Config{
		PollingInterval: 10 * time.Minute,
		Retriever: &fileretriever.Retriever{
			Path: "xxxx.yaml",
		},
	})

	// ...
	goff.ForceRefresh()
	// ...
```
