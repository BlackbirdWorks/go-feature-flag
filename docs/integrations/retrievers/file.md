# File

## Overview
Fetch the configuration from a local file. This retriever is useful when you want to load the configuration from a file in your file system.

## Configure the relay proxy

To configure your relay proxy to use the File System retriever, you need to add the following
configuration to your relay proxy configuration file:

```yaml
# ...
retrievers:
  - kind: file
    path: /goff/my-flags.yaml
# ...
```

| Field name |   Mandatory   | Type   | Default  | Description                                                                                            |
|------------|:-------------:|--------|----------|--------------------------------------------------------------------------------------------------------|
| `kind`     | Yes | string | **none** | **Value should be `file`**.<br/>_This field is mandatory and describes which retriever you are using._ |
| `path`     | Yes | string | **none** | Absolute path to the flag file on the filesystem where the relay proxy runs _(ex: `/goff/my-flags.yaml`)_. In Docker or other containers, this path must exist **inside** the container (see below). |

### Docker and containers

The `path` value is resolved in the relay proxy process filesystem. When you run the relay proxy in a container, mount or copy the **flag file** so that path exists inside the container. You must also mount (or otherwise provide) the **relay proxy configuration file** itself (for example the YAML you pass with `-f` or via your image entrypoint): that file is separate from `retrievers[].path`, and both are usually required for the proxy to start and load flags correctly.

## Configure the GO Module
To configure your GO module to use the File System retriever, you need to add the following
configuration to your `ffclient.Config{}` object:

```go
import 	"github.com/thomaspoignant/go-feature-flag/retriever/file"
// ...
err := ffclient.Init(ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &fileretriever.Retriever{
        Path: "/goff/my-flags.yaml",
    },
})
defer ffclient.Close()
```

| Field      |   Mandatory   | Description                               |
|------------|:-------------:|-------------------------------------------|
| **`Path`** | Yes | location of your file on the file system. |
