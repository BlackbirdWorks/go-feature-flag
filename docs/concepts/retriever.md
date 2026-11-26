# Retrievers

## Overview

A **retriever** loads your feature flag configuration into Semaphore from a local file, URL, cloud storage bucket, Git repository, Kubernetes ConfigMap, or database.

## How it works

- On startup and on a configurable `pollingInterval`, the retriever fetches the latest flag file.
- Loaded flags stay in memory so evaluations never hit external storage on the hot path.
- Keeping flag storage separate from the evaluation engine lets you swap backends without changing application code.

## Supported retrievers

- HTTP(S)
- File System
- Kubernetes ConfigMap
- AWS S3
- MinIO
- Google Cloud Storage
- Azure Blob Storage
- GitHub
- GitLab
- Bitbucket
- MongoDB
- Redis
- PostgreSQL

See [Configure retrievers](../integrations/retrievers/README.md) for setup examples.

## Using multiple retrievers

Configure `retrievers` as a list to combine flags from multiple sources. Semaphore fetches them in parallel and merges them in the order listed.

> [!NOTE]
> If the same flag key appears in multiple retrievers, the later retriever in the list wins.
