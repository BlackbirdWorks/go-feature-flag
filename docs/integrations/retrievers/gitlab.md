# GitLab

## Overview
Fetch the configuration from files stored in a Gitlab repository. This retriever will perform an HTTP Request with your Gitlab configuration on the Gitlab API to get your flags.

> [!TIP]
> GitLab has rate limits, be sure to correctly set your `PollingInterval` to avoid reaching the limit.

> [!NOTE]
> The retriever handles URL encoding, so write the repository slug and the file path as they appear in GitLab:
> - `repositorySlug` / `RepositorySlug`: `group/subgroup/project`, not `group%2Fsubgroup%2Fproject`.
> - `path` / `FilePath`: `flag configs/my flags.yaml`, without replacing spaces with `%20` or `+`.

## Configure the relay proxy

To configure your relay proxy to use the GitLab retriever, you need to add the following
configuration to your relay proxy configuration file:

```yaml
# ...
retrievers:
  - kind: gitlab
    repositorySlug: your-org/your-repo
    path: config/flag/my-flags.yaml
# ...
```

| Field name       |    Mandatory     | Type   | Default              | Description                                                                                                                                                                                              |
|------------------|:----------------:|--------|----------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `kind`           |  Yes   | string | **none**             | **Value should be `gitlab`**.<br/>_This field is mandatory and describes which retriever you are using._                                                                                                 |
| `repositorySlug` |  Yes   | string | **none**             | The repository slug of the GitLab repository where your file is located _(ex: `your-org/your-repo`)_.                                                                                        |
| `path`           |  Yes   | string | **none**             | Path to the file inside the repository _(ex: `config/flag/my-flags.yaml`)_.                                                                                                                              |
| `baseUrl`        | No | string | `https://gitlab.com` | The base URL of your GitLab instance.                                                                                                                                                                    |
| `branch`         | No | string | `main`               | The branch we should check in the repository.                                                                                                                                                            |
| `token`          | No | string | **none**             | GitLab personal access token used to access a private repository ([Create a personal access token](https://docs.gitlab.com/ee/user/profile/personal_access_tokens.html#create-a-personal-access-token)). |
| `timeout`        | No | string | `10000`              | Timeout in millisecond used when calling GitLab.                                                                                                                                                         |

## Configure the GO Module
To configure your GO module to use the GitLab retriever, you need to add the following
configuration to your `ffclient.Config{}` object:

```go
err := ffclient.Init(ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &gitlabretriever.Retriever{
        RepositorySlug: "your-org/your-repo",
        Branch: "main",
        FilePath: "testdata/flag-config.goff.yaml",
        GitlabToken: "XXXX",
        Timeout: 2 * time.Second,
		    BaseURL: "https://gitlab.com",
    },
})
defer ffclient.Close()
```

| Field                |    Mandatory     | Description                                                                |
|----------------------|:----------------:|----------------------------------------------------------------------------|
| **`BaseURL`**        | No | The domain name of your Gitlab instance <br/>Default: `https://gitlab.com` |
| **`RepositorySlug`** |  Yes   | Your Gitlab slug `org/repo-name`.                                          |
| **`FilePath`**       |  Yes   | The path of your file.                                                     |
| **`Branch`**         | No | The branch where your file is.<br/>Default: `main`                         |
| **`GitlabToken`**    | No | GitLab token is used to access a private repository                        |
| **`Timeout`**        | No | Timeout for the HTTP call <br/>Default: 10 seconds                         |
