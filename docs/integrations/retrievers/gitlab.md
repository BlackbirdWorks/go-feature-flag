# GitLab

> [!NOTE]
> The relay proxy does not run retrievers: it reads its control plane from a store connection, see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). This page is about the retriever of the Go module.

## Overview

Fetch flag configurations from a file stored in a GitLab repository via the GitLab API.

> [!TIP]
> GitLab enforces rate limits; set `PollingInterval` carefully to avoid hitting them.

> [!NOTE]
> The retriever handles URL encoding, so write `repositorySlug` and `path` as they appear in GitLab:
> - `repositorySlug` / `RepositorySlug`: `group/subgroup/project`, not `group%2Fsubgroup%2Fproject`.
> - `path` / `FilePath`: `flag configs/my flags.yaml`, without replacing spaces with `%20` or `+`.

## Configure the Go module

Add the retriever to `ffclient.Config{}`:

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &gitlabretriever.Retriever{
        RepositorySlug: "your-org/your-repo",
        Branch:         "main",
        FilePath:       "testdata/flag-config.yaml",
        GitlabToken:    "XXXX",
        Timeout:        2 * time.Second,
        BaseURL:        "https://gitlab.com",
    },
})
defer client.Close()
```

| Field                | Mandatory | Description |
|----------------------|:---------:|-------------|
| **`BaseURL`**        | No        | Base URL of your GitLab instance.<br/>Default: `https://gitlab.com` |
| **`RepositorySlug`** | Yes       | Your GitLab slug `org/repo-name`. |
| **`FilePath`**       | Yes       | Path to your flag file. |
| **`Branch`**         | No        | Branch where your file lives.<br/>Default: `main` |
| **`GitlabToken`**    | No        | GitLab token used to access a private repository. |
| **`Timeout`**        | No        | Timeout for the HTTP call.<br/>Default: `10 seconds` |
