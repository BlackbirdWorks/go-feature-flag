# Google Cloud Pub/Sub

## Overview

Export evaluation data to a Google Cloud Pub/Sub topic.

> [!NOTE]
> Google Cloud Pub/Sub is a streaming (queue-style) exporter: it sends events as soon as it receives them instead of batching them in bulk.

## Configure the relay proxy

Add the exporter to the `exporters` map of the [bootstrap file](../../relay-proxy/bootstrap-config.md):

```yaml
version: 2
exporters:
  evaluations:
    kind: pubsub
    projectId: my-project
    topic: evaluation-events
```

- `projectId` and `topic` (required).
- Optional: `googleCredentials` (a secret).

The settings that every exporter takes (`flushInterval`, `maxEventInMemory`, `eventType`, `meta`, `overflowPolicy`, `retry` and the other pipeline settings) are in the [bootstrap file reference](../../relay-proxy/bootstrap-config.md) (section `exporters.<name>`).

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
