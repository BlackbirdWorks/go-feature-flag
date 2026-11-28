# AWS Kinesis

## Overview

Export evaluation data to an AWS Kinesis stream.

> [!NOTE]
> AWS Kinesis is a streaming (queue-style) exporter: it sends events as soon as it receives them instead of batching them in bulk.

## Configure the relay proxy

Add the exporter to the `exporters` map of the [bootstrap file](../../relay-proxy/bootstrap-config.md):

```yaml
version: 2
connections:
  stream:
    type: kinesis
    region: eu-west-1
    stream: evaluation-events
exporters:
  evaluations:
    kind: kinesis
    connection: stream
```

- `connection` (required): a `kinesis` connection.
- The stream: `streamName` or `streamArn` on the exporter, or `stream` on the connection.
- `format` is accepted and ignored: the events are always JSON.

The settings that every exporter takes (`flushInterval`, `maxEventInMemory`, `eventType`, `meta`, `overflowPolicy`, `retry` and the other pipeline settings) are in the [bootstrap file reference](../../relay-proxy/bootstrap-config.md) (section `exporters.<name>`).

## Configure the Go module

Add the following configuration to your `ffclient.Config{}` object:

```go
awsConfig, _ := config.LoadDefaultConfig(context.Background())
ffConfig := ffclient.Config{
    // ...
    DataExporter: ffclient.DataExporter{
        // ...
        Exporter: &kinesisexporter.Exporter{
            Settings: kinesisexporter.NewSettings(
                kinesisexporter.WithStreamName("test-stream"),
                kinesisexporter.WithPartitionKey(func(_ context.Context, _ exporter.ExportableEvent) string {
                    return "0"
                }),
                kinesisexporter.WithExplicitHashKey("0"),
            ),
            AwsConfig: &awsConfig, // optional custom AWS configuration
        },
    },
    // ...
}
client, err := ffclient.New(ffConfig)
defer client.Close()
```

| Field             | Mandatory | Description                                                                                                                                                                                                           |
|-------------------|:---------:|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `StreamName`      | Yes       | Name of the Kinesis stream to publish messages to (set via `WithStreamName`, or set a stream ARN via `WithStreamArn`).                                                                                                |
| `PartitionKey`    | No        | Function (`func(context.Context, exporter.ExportableEvent) string`) returning the partition key for an event. Defaults to `"default"`, which sends all events to a single shard unless overridden.                   |
| `ExplicitHashKey` | No        | String key (`WithExplicitHashKey`) identifying the shard hash key for the record. Overrides `PartitionKey` routing.                                                                                                   |
| `AwsConfig`       | No        | An instance of `*aws.Config` holding additional AWS SDK settings.                                                                                                                                                     |
