# Bitbucket

## Overview
Fetch the configuration from files stored in a GIT repository.

> [!TIP]
> Bitbucket has rate limits, be sure to correctly set your `PollingInterval` to avoid reaching the limit.

## Configure the relay proxy

To configure your relay proxy to use the Bitbucket retriever, you need to add the following
configuration to your relay proxy configuration file:

```yaml
# ...
retrievers:
  - kind: bitbucket
    repositorySlug: your-org/your-repo
    path: config/flag/my-flags.yaml
# ...
```

| Field name       |    Mandatory     | Type   | Default              | Description                                                                                                                                                                       |
|------------------|:----------------:|--------|----------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `kind`           |  Yes   | string | **none**             | Value should be **`bitbucket`**.<br/>_This field is mandatory and describes which retriever you are using._                                                                       |
| `repositorySlug` |  Yes   | string | **none**             | The repository slug of the Bitbucket repository where your file is located _(ex: `your-org/your-repo`)_.                                                              |
| `path`           |  Yes   | string | **none**             | Path to the file inside the repository _(ex: `config/flag/my-flags.yaml`)_.                                                                                                       |
| `baseUrl`        | No | string | `https://gitlab.com` | The base URL of your Bitbucket instance<br/>By default we are using the public API `https://api.bitbucket.org`.                                                                   |
| `branch`         | No | string | `main`               | The branch we should check in the repository.                                                                                                                                     |
| `token`          | No | string | **none**             | Bitbucket token used to access a private repository ([_Create a Repository Access Token_](https://support.atlassian.com/bitbucket-cloud/docs/create-a-repository-access-token/)). |
| `timeout`        | No | string | `10000`              | Timeout in millisecond used when calling GitLab.                                                                                                                                  |

## Configure the GO Module
To configure your GO module to use the Bitbucket retriever, you need to add the following
configuration to your `ffclient.Config{}` object:

```go
err := ffclient.Init(ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &bitbucketretriever.Retriever{
        RepositorySlug: "your-org/your-repo",
        Branch: "main",
        FilePath: "testdata/flag-config.goff.yaml",
        BitBucketToken: "XXXX",
        Timeout: 2 * time.Second,
    },
})
defer ffclient.Close()
```

| Field                |    Mandatory     | Description                                                                          |
|----------------------|:----------------:|--------------------------------------------------------------------------------------|
| **`RepositorySlug`** |  Yes   | Your Gitlab slug `org/repo-name`.                                                    |
| **`FilePath`**       |  Yes   | The path of your file.                                                               |
| **`BaseURL`**        | No | The domain name of your Bitbucket instance <br/>Default: `https://api.bitbucket.org` |
| **`Branch`**         | No | The branch where your file is.<br/>Default: `main`                                   |
| **`BitBucketToken`** | No | Bitbucket token is used to access a private repository                               |
| **`Timeout`**        | No | Timeout for the HTTP call <br/>Default: 10 seconds                                   |
