# File

> [!NOTE]
> The relay proxy does not run retrievers: it reads its control plane from a directory (`type: file`) connection, see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). The `retrievers` configuration of this page is not read by the
> relay. The Go module is not affected.

## Overview

Load flag configurations from a local file on the filesystem.

## Configure the relay proxy

Add this configuration to your relay proxy config file:

```yaml
# ...
retrievers:
  - kind: file
    path: /semaphore/my-flags.yaml
# ...
```

| Field name | Mandatory | Type   | Default  | Description |
|------------|:---------:|--------|----------|-------------|
| `kind`     | Yes       | string | **none** | **Value should be `file`.**<br/>_Mandatory field identifying the retriever._ |
| `path`     | Yes       | string | **none** | Path to the flag file on the filesystem where the relay proxy runs _(ex: `/semaphore/my-flags.yaml`)_. In containers, this path must exist **inside** the container (see below). |

### Docker and containers

`path` is resolved inside the relay proxy process filesystem. When running in a container, mount or copy the **flag file** so that path exists inside the container. You must also mount or provide the **relay proxy configuration file** itself (for example via `-f` or your image entrypoint); it is separate from `retrievers[].path`, and both are needed for the proxy to start and load flags.

## Configure the Go module

Add the retriever to `ffclient.Config{}`:

```go
import "github.com/blackbirdworks/semaphore/retriever/fileretriever"
// ...
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &fileretriever.Retriever{
        Path: "/semaphore/my-flags.yaml",
    },
})
defer client.Close()
```

| Field      | Mandatory | Description |
|------------|:---------:|-------------|
| **`Path`** | Yes       | Location of your flag file on the filesystem. |
