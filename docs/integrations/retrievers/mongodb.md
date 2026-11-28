# MongoDB

> [!NOTE]
> The relay proxy does not run retrievers: it reads its control plane from a store connection, see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). This page is about the retriever of the Go module.

## Overview

Load flag configurations from a MongoDB collection.

## MongoDB collection format

Store each flag as a JSON document matching the format in [create flags](../../configure-flags/create-flags.md), with an extra `flag` field containing the flag name.

The retriever reads all documents from the collection at once.

### Example

```json
{
    "flag": "new-admin-access",
    "variations": {
        "default_var": false,
        "false_var": false,
        "true_var": true
    },
    "defaultRule": {
        "percentage": {
            "false_var": 70,
            "true_var": 30
        }
    }
}
```

## Configure the Go module

Add the retriever to `ffclient.Config{}`:

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &mongodbretriever.Retriever{
        Collection: "featureFlags",
        Database:   "appConfig",
        URI:        "mongodb://root:example@127.0.0.1:27017/",
    },
})
defer client.Close()
```

| Field            | Mandatory | Description |
|------------------|:---------:|-------------|
| **`Collection`** | Yes       | Name of the collection where your flags are stored. |
| **`Database`**   | Yes       | Name of the MongoDB database containing the collection. |
| **`URI`**        | Yes       | Connection URI of your MongoDB instance. |
