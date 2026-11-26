# Google Cloud Pub/Sub

## Overview

Export evaluation data to a Google Cloud Pub/Sub topic.

> [!NOTE]
> Google Cloud Pub/Sub is a streaming (queue-style) exporter: it sends events as soon as it receives them instead of batching them in bulk.

## Configure the relay proxy

Add the following configuration to your relay proxy configuration file:

```yaml
# ...
exporters:
  - kind: pubsub
    projectID: "my-project-id"
    topic: "semaphore-feature-events"
# ...
```

| Field name  | Mandatory | Type   | Default  | Description                                                                                         |
|-------------|:---------:|--------|----------|-----------------------------------------------------------------------------------------------------|
| `kind`      | Yes       | string | **none** | Value should be `pubsub`.<br/>_This field is mandatory and describes which exporter you are using._ |
| `projectID` | Yes       | string | **none** | ID of your GCP project.                                                                             |
| `topic`     | Yes       | string | **none** | Topic name on which messages are published.                                                         |

## Configure the Go module

Add the following configuration to your `ffclient.Config{}` object:

> [!NOTE]
> **Migration from v1**: If you are migrating from the legacy `pubsubexporter` (v1), change the import and struct name from `pubsubexporter.Exporter` to `pubsubexporterv2.Exporter`. The v2 exporter uses `cloud.google.com/go/pubsub/v2` for improved performance and additional features.

```go
ffConfig := ffclient.Config{
    // ...
    DataExporter: ffclient.DataExporter{
        // ...
        Exporter: &pubsubexporterv2.Exporter{
            ProjectID:             "project-id", // required
            Topic:                 "topic",      // required
            Options:               []option.ClientOption{...},
            PublishSettings:       &pubsub.PublishSettings{...},
            EnableMessageOrdering: true,
        },
    },
    // ...
}
client, err := ffclient.New(ffConfig)
defer client.Close()
```

| Field                   | Mandatory | Description                                                                                             |
|-------------------------|:---------:|---------------------------------------------------------------------------------------------------------|
| `ProjectID`             | Yes       | ID of your GCP project (available in the GCP console).                                                  |
| `Topic`                 | Yes       | Name of the topic on which messages are published.                                                      |
| `Options`               | No        | Pub/Sub client options (see [docs](https://pkg.go.dev/google.golang.org/api/option)).                   |
| `PublishSettings`       | No        | Publisher settings (see [docs](https://pkg.go.dev/cloud.google.com/go/pubsub/v2#PublishSettings)).      |
| `EnableMessageOrdering` | No        | Enables delivery of ordered keys.                                                                       |
