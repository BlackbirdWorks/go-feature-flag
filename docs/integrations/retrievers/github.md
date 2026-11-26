# GitHub

## Overview

Fetch flag configurations from a file stored in a GitHub repository via the GitHub API.

> [!NOTE]
> GitHub enforces rate limits; set `PollingInterval` carefully to avoid hitting them.
>
> **If the rate limit is reached, the retriever logs an error and pauses polling until GitHub resets the limit.**

## Configure the relay proxy

Add this configuration to your relay proxy config file:

```yaml
# ...
retrievers:
  - kind: github
    repositorySlug: your-org/your-repo
    path: config/flag/my-flags.yaml
# ...
```

| Field name                | Mandatory | Type   | Default                  | Description |
|---------------------------|:---------:|--------|--------------------------|-------------|
| `kind`                    | Yes       | string | **none**                 | **Value should be `github`.**<br/>_Mandatory field identifying the retriever._ |
| `repositorySlug`          | Yes       | string | **none**                 | Repository slug of the GitHub repository _(ex: `your-org/your-repo`)_. |
| `path`                    | Yes       | string | **none**                 | Path to the file inside the repository _(ex: `config/flag/my-flags.yaml`)_. |
| `branch`                  | No        | string | `main`                   | Branch to read in the repository. |
| `token`                   | No        | string | **none**                 | GitHub PAT with `repo` permission for private repositories ([create a token](https://docs.github.com/en/free-pro-team@latest/github/authenticating-to-github/creating-a-personal-access-token)). Mutually exclusive with the GitHub App fields below. |
| `baseUrl`                 | No        | string | `https://api.github.com` | Base URL for the GitHub API. Use for GitHub Enterprise or self-hosted instances _(ex: `https://github.acme.com/api/v3`)_. |
| `timeout`                 | No        | int    | `10000`                  | Timeout in milliseconds when calling GitHub. |
| `githubAppId`             | No        | int    | **none**                 | GitHub App ID. Required for GitHub App auth (see [GitHub App authentication](#github-app-authentication)). Mutually exclusive with `token`. |
| `githubInstallationId`    | No        | int    | **none**                 | GitHub App installation ID. Required for GitHub App auth. |
| `githubAppPrivateKey`     | No        | string | **none**                 | Inline PEM-encoded RSA private key for the GitHub App. Use this or `githubAppPrivateKeyFile`, not both. |
| `githubAppPrivateKeyFile` | No        | string | **none**                 | Path to a PEM file containing the GitHub App private key (useful for Kubernetes secret mounts). Takes precedence over `githubAppPrivateKey` if both are set. |

## Configure the Go module

Add the retriever to `ffclient.Config{}`:

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &githubretriever.Retriever{
        RepositorySlug: "your-org/your-repo",
        Branch:         "main",
        FilePath:       "testdata/flag-config.yaml",
        GithubToken:    "XXXX",
        Timeout:        2 * time.Second,
    },
})
defer client.Close()
```

| Field                | Mandatory | Description |
|----------------------|:---------:|-------------|
| **`RepositorySlug`** | Yes       | Your GitHub slug `org/repo-name`. |
| **`FilePath`**       | Yes       | Path to your flag file. |
| **`Branch`**         | No        | Branch where your file lives.<br/>Default: `main` |
| **`GithubToken`**    | No        | GitHub PAT with `repo` permission ([create a token](https://docs.github.com/en/free-pro-team@latest/github/authenticating-to-github/creating-a-personal-access-token)). Mutually exclusive with the GitHub App fields. |
| **`BaseURL`**        | No        | Base URL for the GitHub API (for GitHub Enterprise or self-hosted instances, ex: `https://github.acme.com/api/v3`).<br/>Default: `https://api.github.com` |
| **`Timeout`**        | No        | Timeout for the HTTP call.<br/>Default: `10 seconds` |
| **`AppID`**          | No        | GitHub App ID. Required for GitHub App auth (see [GitHub App authentication](#github-app-authentication)). Mutually exclusive with `GithubToken`. |
| **`InstallationID`** | No        | GitHub App installation ID. Required for GitHub App auth. |
| **`PrivateKey`**     | No        | Inline PEM-encoded RSA private key for the GitHub App. Use this or `PrivateKeyFile`, not both. |
| **`PrivateKeyFile`** | No        | Path to a PEM file containing the GitHub App private key. Takes precedence over `PrivateKey` if both are set. |

## GitHub App authentication

You can authenticate as a [GitHub App](https://docs.github.com/en/apps/creating-github-apps/about-creating-github-apps/about-creating-github-apps) instead of using a personal access token. This removes the dependency on a user account and gives you fine-grained, repo-scoped permissions.

The retriever handles the OAuth flow internally: it signs a short-lived JWT with the app's private key, exchanges it for an installation token, caches the token, and refreshes it automatically before expiry.

**GitHub App prerequisites:**
- Repository permission: **Contents: read-only** (Metadata: read is implicit).
- Install the app on the repository holding your flag file.
- Generate a private key in the app settings and store it securely (e.g., in a Kubernetes secret or AWS Secrets Manager).
- Note the **App ID** (on the app settings page) and **Installation ID** (in the installation settings URL: `https://github.com/organizations/<org>/settings/installations/<id>`).

### Relay proxy configuration

```yaml
# ...
retrievers:
  - kind: github
    repositorySlug: myorg/my-flags-repo
    path: flags/config.yaml
    githubAppId: 12345
    githubInstallationId: 67890
    # Option A: path to a PEM file (recommended for Kubernetes secret mounts)
    githubAppPrivateKeyFile: /var/secrets/github-app.pem
    # Option B: inline PEM string — inject from a secret manager via environment variable
    # githubAppPrivateKey: <contents of your .pem file>
# ...
```

> [!NOTE]
> `token` / `githubToken` and the GitHub App fields (`githubAppId`, `githubInstallationId`, `githubAppPrivateKey` / `githubAppPrivateKeyFile`) are mutually exclusive. Validation rejects configurations that supply both.

### Go module configuration

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &githubretriever.Retriever{
        RepositorySlug: "myorg/my-flags-repo",
        FilePath:       "flags/config.yaml",
        AppID:          12345,
        InstallationID: 67890,
        // Option A: path to a PEM file (recommended)
        PrivateKeyFile: "/var/secrets/github-app.pem",
        // Option B: inline PEM string loaded from a secret at runtime
        // PrivateKey: os.Getenv("GITHUB_APP_PRIVATE_KEY"),
    },
})
defer client.Close()
```

## GitHub Enterprise / self-hosted instances

Set `baseUrl` (YAML) or `BaseURL` (Go) to point to your GitHub Enterprise or self-hosted API endpoint.

### Relay proxy configuration

```yaml
# ...
retrievers:
  - kind: github
    repositorySlug: myorg/myrepo
    path: config/flag/my-flags.yaml
    baseUrl: "https://github.acme.com/api/v3"
# ...
```

### Go module configuration

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &githubretriever.Retriever{
        RepositorySlug: "myorg/myrepo",
        Branch:         "main",
        FilePath:       "config/flag/my-flags.yaml",
        BaseURL:        "https://github.acme.com/api/v3",
        GithubToken:    "XXXX",
    },
})
defer client.Close()
```
