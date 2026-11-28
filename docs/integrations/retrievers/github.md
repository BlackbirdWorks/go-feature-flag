# GitHub

> [!NOTE]
> The relay proxy does not run retrievers: it reads its control plane from a store connection, see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). This page is about the retriever of the Go module.

## Overview

Fetch flag configurations from a file stored in a GitHub repository via the GitHub API.

> [!NOTE]
> GitHub enforces rate limits; set `PollingInterval` carefully to avoid hitting them.
>
> **If the rate limit is reached, the retriever logs an error and pauses polling until GitHub resets the limit.**

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
