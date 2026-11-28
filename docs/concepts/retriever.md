# Retrievers

## Overview

A **retriever** loads flag configuration into the Go module (`ffclient`) from a local file, URL, cloud storage bucket,
Git repository, Kubernetes ConfigMap, or database.

The relay proxy does not run retrievers. It reads flag sets from a control plane store (a directory or an S3 bucket)
that [`semaphore-cli publish`](../tooling/publish.md) writes, see [control-plane polling](control-plane-polling.md) and
[connections](connections.md). The other retriever kinds return to the relay with issue #57.

## How it works in the Go module

- On startup and on a configurable `PollingInterval`, the retriever fetches the latest flag file.
- Loaded flags stay in memory so evaluations never hit external storage on the hot path.
- Keeping flag storage separate from the evaluation engine lets you swap backends without changing application code.

## Available retrievers

HTTP(S), File System, Kubernetes ConfigMap, AWS S3, MinIO, Google Cloud Storage, Azure Blob Storage, GitHub, GitLab,
Bitbucket, MongoDB, Redis and PostgreSQL. See [Store your flags](../integrations/retrievers/README.md) for the setup of
each.

## Using multiple retrievers

Set `Retrievers` in `ffclient.Config` to combine flags from several sources. Semaphore fetches them in parallel and
merges them in the order listed.

> [!NOTE]
> If the same flag key appears in multiple retrievers, the later retriever in the list wins.
