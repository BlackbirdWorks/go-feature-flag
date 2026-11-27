# MongoDB

> [!NOTE]
> The relay proxy does not run retrievers any more: it reads a control plane store (S3 or a directory), see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). The relay configuration examples on this page are version 1 and
> are not read by the relay; the retriever kinds come back to the relay with the control plane connections (issue #57).
> The Go module is not affected.

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

## Configure the relay proxy

Add this configuration to your relay proxy config file:

```yaml
# ...
retrievers:
  - kind: mongodb
    uri: mongodb://root:example@127.0.0.1:27017/
    database: appConfig
    collection: featureFlags
# ...
```

| Field name   | Mandatory | Type   | Default  | Description |
|--------------|:---------:|--------|----------|-------------|
| `kind`       | Yes       | string | **none** | Value should be **`mongodb`**.<br/>_Mandatory field identifying the retriever._ |
| `uri`        | Yes       | string | **none** | MongoDB connection URI. |
| `database`   | Yes       | string | **none** | Name of the **database** where flags are stored. |
| `collection` | Yes       | string | **none** | Name of the **collection** where flags are stored. |

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
