# GitLab

> [!NOTE]
> The relay proxy does not run this retriever: its use in the relay returns with issue #57, on the control plane
> connections of the [bootstrap file](../../relay-proxy/bootstrap-config.md). The relay configuration of this page is not
> read by the relay. The Go module is not affected.

## Overview

Fetch flag configurations from a file stored in a GitLab repository via the GitLab API.

> [!TIP]
> GitLab enforces rate limits; set `PollingInterval` carefully to avoid hitting them.

> [!NOTE]
> The retriever handles URL encoding, so write `repositorySlug` and `path` as they appear in GitLab:
> - `repositorySlug` / `RepositorySlug`: `group/subgroup/project`, not `group%2Fsubgroup%2Fproject`.
> - `path` / `FilePath`: `flag configs/my flags.yaml`, without replacing spaces with `%20` or `+`.

## Configure the relay proxy

Add this configuration to your relay proxy config file:

```yaml
# ...
retrievers:
  - kind: gitlab
    repositorySlug: your-org/your-repo
    path: config/flag/my-flags.yaml
# ...
```

| Field name       | Mandatory | Type   | Default              | Description |
|------------------|:---------:|--------|----------------------|-------------|
| `kind`           | Yes       | string | **none**             | **Value should be `gitlab`.**<br/>_Mandatory field identifying the retriever._ |
| `repositorySlug` | Yes       | string | **none**             | Repository slug of the GitLab repository _(ex: `your-org/your-repo`)_. |
| `path`           | Yes       | string | **none**             | Path to the file inside the repository _(ex: `config/flag/my-flags.yaml`)_. |
| `baseUrl`        | No        | string | `https://gitlab.com` | Base URL of your GitLab instance. |
| `branch`         | No        | string | `main`               | Branch to read in the repository. |
| `token`          | No        | string | **none**             | GitLab personal access token for private repositories ([create a token](https://docs.gitlab.com/ee/user/profile/personal_access_tokens.html#create-a-personal-access-token)). |
| `timeout`        | No        | int    | `10000`              | Timeout in milliseconds when calling GitLab. |

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
