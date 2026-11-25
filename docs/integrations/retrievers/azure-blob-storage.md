# Azure Blob Storage

## Overview
Retrieves the configuration from an Azure Blob Storage. This retriever is useful when you are using Azure and want to use Azure Blob Storage to store your configuration files.

## Configure the relay proxy

To configure your relay proxy to use the Azure Blob Storage retriever, you need to add the following
configuration to your relay proxy configuration file:

```yaml
# ...
retrievers:
  - kind: azureBlobStorage
    container: my-featureflag-container
    accountName: my-featureflag-account
    object: flag/flags.yaml
# ...
```

| Field name    |    Mandatory     | Type   | Default  | Description                                                                                                        |
|---------------|:----------------:|--------|----------|--------------------------------------------------------------------------------------------------------------------|
| `kind`        |  Yes   | string | **none** | Value should be **`azureBlobStorage`**.<br/>_This field is mandatory and describes which retriever you are using._ |
| `container`   |  Yes   | string | **none** | This is the name of your Azure Blob Storage container _(ex: `my-featureflag-container`)_.                          |
| `accountName` |  Yes   | string | **none** | This is the name of your Azure Blob Storage account.                                                               |
| `object`      |  Yes   | string | **none** | Location of your configuration file.                                                                               |
| `accountKey`  | No | string | **none** | This is the secret key of your Azure Blob Storage account.                                                         |

## Configure the GO Module
To configure your GO module to use the Azure Blob Storage retriever, you need to add the following
configuration to your `ffclient.Config{}` object:

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &azblobstorageretriever.Retriever{
        Container:    "your-container",
        AccountName:  "your-account-name",
        AccountKey:   "your-account-key",
        Object:       "your_location/feature-flags.json",
    },
})
defer client.Close()
```

| Field             |    Mandatory     | Description                                    |
|-------------------|:----------------:|------------------------------------------------|
| **`Container`**   |  Yes   | Name of the Azure Blob Storage container       |
| **`Object`**      |  Yes   | Name of the feature flag file in the container |
| **`AccountName`** |  Yes   | Azure Storage Account Name                     |
| **`AccountKey`**  | No | Storage Account Key                            |
| **`ServiceURL`**  | No | Custom service URL                             |

## Error Handling

The `Retrieve` method returns an error if:
- `AccountName` is empty
- `Container` or `Object` is not specified
- There's an issue initializing the Azure client.
- There's a problem downloading or reading the file from Azure Blob Storage.
