# Azure Blob Storage

> [!NOTE]
> The relay proxy does not run retrievers: it reads its control plane from a store connection, see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). This page is about the retriever of the Go module.

## Overview

Load flag configurations from Azure Blob Storage.

## Configure the Go module

Add the retriever to `ffclient.Config{}`:

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &azblobstorageretriever.Retriever{
        Container:   "your-container",
        AccountName: "your-account-name",
        AccountKey:  "your-account-key",
        Object:      "your_location/feature-flags.json",
    },
})
defer client.Close()
```

| Field             | Mandatory | Description |
|-------------------|:---------:|-------------|
| **`Container`**   | Yes       | Name of the Azure Blob Storage container. |
| **`Object`**      | Yes       | Name of the feature flag file in the container. |
| **`AccountName`** | Yes       | Azure Storage account name. |
| **`AccountKey`**  | No        | Storage account key. |
| **`ServiceURL`**  | No        | Custom service URL _(defaults to `https://<account>.blob.core.windows.net/`)_. |

## Error handling

- `Init` returns an error if `AccountName` is empty or initializing the Azure client fails.
- `Retrieve` returns an error if:
  - The Azure client is not initialized
  - `Container` or `Object` is not specified
  - Downloading or reading the file from Azure Blob Storage fails
