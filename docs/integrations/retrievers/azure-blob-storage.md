# Azure Blob Storage

> [!NOTE]
> The relay proxy does not run this retriever: its use in the relay returns with issue #57, on the control plane
> connections of the [bootstrap file](../../relay-proxy/bootstrap-config.md). The relay configuration of this page is not
> read by the relay. The Go module is not affected.

## Overview

Load flag configurations from Azure Blob Storage.

## Configure the relay proxy

Add this configuration to your relay proxy config file:

```yaml
# ...
retrievers:
  - kind: azureBlobStorage
    container: my-featureflag-container
    accountName: my-featureflag-account
    object: flag/flags.yaml
# ...
```

| Field name    | Mandatory | Type   | Default  | Description |
|---------------|:---------:|--------|----------|-------------|
| `kind`        | Yes       | string | **none** | Value should be **`azureBlobStorage`**.<br/>_Mandatory field identifying the retriever._ |
| `container`   | Yes       | string | **none** | Name of your Azure Blob Storage container _(ex: `my-featureflag-container`)_. |
| `accountName` | Yes       | string | **none** | Name of your Azure Blob Storage account. |
| `object`      | Yes       | string | **none** | Location of your configuration file. |
| `accountKey`  | No        | string | **none** | Secret key of your Azure Blob Storage account. If omitted, `DefaultAzureCredential` is used. |

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
