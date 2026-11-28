# Bitbucket

> [!NOTE]
> The relay proxy does not run retrievers: it reads its control plane from a store connection, see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). This page is about the retriever of the Go module.

## Overview

Fetch flag configurations from a file stored in a Bitbucket Git repository.

> [!TIP]
> Bitbucket enforces rate limits; set `PollingInterval` carefully to avoid hitting them.

## Configure the Go module

Add the retriever to `ffclient.Config{}`:

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &bitbucketretriever.Retriever{
        RepositorySlug: "your-org/your-repo",
        Branch:         "main",
        FilePath:       "testdata/flag-config.yaml",
        BitBucketToken: "XXXX",
        Timeout:        2 * time.Second,
    },
})
defer client.Close()
```

| Field                | Mandatory | Description |
|----------------------|:---------:|-------------|
| **`RepositorySlug`** | Yes       | Your Bitbucket slug `org/repo-name`. |
| **`FilePath`**       | Yes       | Path to your flag file. |
| **`BaseURL`**        | No        | Domain/base URL of your Bitbucket instance.<br/>Default: `https://api.bitbucket.org` |
| **`Branch`**         | No        | Branch where your file lives.<br/>Default: `main` |
| **`BitBucketToken`** | No        | Bitbucket token used to access a private repository. |
| **`Timeout`**        | No        | Timeout for the HTTP call.<br/>Default: `10 seconds` |
