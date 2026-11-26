# Google Cloud PubSub

## Overview
Export evaluation data inside a GCP PubSub topic.

> [!NOTE]
> Google Cloud PubSub is an exporter of type queue, it means that it send events as soon as he receives them it does not work in bulk but in near real time.

## Configure the relay proxy

To configure your relay proxy to use the Google Cloud PubSub exporter, you need to add the following
configuration to your relay proxy configuration file:

```yaml
# ...
exporters:
  - kind: pubsub
    projectID: "my-project-id"
    topic: "semaphore-feature-events"
# ...
```

| Field name  |   Mandatory   | Type   | Default  | Description                                                                                              |
|-------------|:-------------:|--------|----------|----------------------------------------------------------------------------------------------------------|
| `kind`      | Yes | string | **none** | **Value should be `pubsub`**.<br/>_This field is mandatory and describes which retriever you are using._ |
| `projectID` | Yes | string | **none** | Value should be ID of GCP project you are using.                                                         |
| `topic`     | Yes | string | **none** | Topic name on which messages will be published.                                                          |

## Configure the Go Module
To configure your Go module to use the Google Cloud PubSub exporter, you need to add the following
configuration to your `ffclient.Config{}` object:

> [!NOTE]
> **Migration from v1**
> If you are migrating from the legacy `pubsubexporter` (v1), simply change the import and struct name from `pubsubexporter.Exporter` to `pubsubexporterv2.Exporter`. The v2 exporter uses `cloud.google.com/go/pubsub/v2` which provides improved performance and additional features.

```go
config := ffclient.Config{
   // ...
    DataExporter: ffclient.DataExporter{
        // ...
        Exporter: &pubsubexporterv2.Exporter{
            ProjectID: "project-id", // required
            Topic: "topic", // required
            Options: []option.ClientOption{...},
            PublishSettings: &pubsub.PublishSettings{...},
            EnableMessageOrdering: true,
        },
    },
    // ...
}
client, err := ffclient.New(config)
defer client.Close()
```

| Field                   |    Mandatory     | Description                                                                                          |
|-------------------------|:----------------:|------------------------------------------------------------------------------------------------------|
| `ProjectID `            |  Yes   | ID of GCP project.<br/>_You can find it in your GCP console_                                         |
| `Topic `                |  Yes   | Name of topic on which messages will be published                                                    |
| `Options`               | No | PubSub client options *(see [docs](https://pkg.go.dev/google.golang.org/api/option))*                |
| `PublishSettings`       | No | Publisher related settings *(see [docs](https://pkg.go.dev/cloud.google.com/go/pubsub/v2#PublishSettings))* |
| `EnableMessageOrdering` | No | Enables delivery of ordered keys                                                                     |
