# File

> [!NOTE]
> The relay proxy does not run retrievers: it reads its control plane from a store connection, see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). This page is about the retriever of the Go module.

## Overview

Load flag configurations from a local file on the filesystem.

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
