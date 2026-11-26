# AWS Kinesis

## Overview

Export evaluation data to an AWS Kinesis stream.

> [!NOTE]
> AWS Kinesis is a streaming (queue-style) exporter: it sends events as soon as it receives them instead of batching them in bulk.

## Configure the relay proxy

Add the following configuration to your relay proxy configuration file:

```yaml
# ...
exporters:
  - kind: kinesis
    streamArn: "arn:aws:kinesis:us-east-1:XXXX:stream/test-stream"
# ...
```

| Field name   | Mandatory | Type   | Default  | Description                                                                                                          |
|--------------|:---------:|--------|----------|----------------------------------------------------------------------------------------------------------------------|
| `kind`       | Yes       | string | **none** | Value should be `kinesis`.<br/>_This field is mandatory and describes which exporter you are using._                  |
| `streamArn`  | Yes       | string | **none** | The ARN of your Kinesis stream. Required unless `streamName` is set.                                                 |
| `streamName` | No        | string | **none** | The name of your Kinesis stream. Required if `streamArn` is not set.                                                 |

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
