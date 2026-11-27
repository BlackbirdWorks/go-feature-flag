# Bitbucket

> [!NOTE]
> The relay proxy does not run retrievers any more: it reads a control plane store (S3 or a directory), see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). The relay configuration examples on this page are version 1 and
> are not read by the relay; the retriever kinds come back to the relay with the control plane connections (issue #57).
> The Go module is not affected.

## Overview

Fetch flag configurations from a file stored in a Bitbucket Git repository.

> [!TIP]
> Bitbucket enforces rate limits; set `PollingInterval` carefully to avoid hitting them.

## Configure the relay proxy

Add this configuration to your relay proxy config file:

```yaml
# ...
retrievers:
  - kind: bitbucket
    repositorySlug: your-org/your-repo
    path: config/flag/my-flags.yaml
# ...
```

| Field name       | Mandatory | Type   | Default                     | Description |
|------------------|:---------:|--------|-----------------------------|-------------|
| `kind`           | Yes       | string | **none**                    | Value should be **`bitbucket`**.<br/>_Mandatory field identifying the retriever._ |
| `repositorySlug` | Yes       | string | **none**                    | Repository slug of the Bitbucket repository _(ex: `your-org/your-repo`)_. |
| `path`           | Yes       | string | **none**                    | Path to the file inside the repository _(ex: `config/flag/my-flags.yaml`)_. |
| `baseUrl`        | No        | string | `https://api.bitbucket.org` | Base URL of your Bitbucket instance.<br/>Defaults to the public API `https://api.bitbucket.org`. |
| `branch`         | No        | string | `main`                      | Branch to read in the repository. |
| `token`          | No        | string | **none**                    | Bitbucket token used to access a private repository ([Create a Repository Access Token](https://support.atlassian.com/bitbucket-cloud/docs/create-a-repository-access-token/)). |
| `timeout`        | No        | int    | `10000`                     | Timeout in milliseconds when calling Bitbucket. |

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
