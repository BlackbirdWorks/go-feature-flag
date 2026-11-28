# AWS SQS

## Overview

Export evaluation data to an AWS SQS queue.

> [!NOTE]
> AWS SQS is a streaming (queue-style) exporter: it sends events as soon as it receives them instead of batching them in bulk.

## Configure the relay proxy

Add the exporter to the `exporters` map of the [bootstrap file](../../relay-proxy/bootstrap-config.md):

```yaml
version: 2
connections:
  queue:
    type: sqs
    region: eu-west-1
    queueUrl: https://sqs.eu-west-1.amazonaws.com/123456789012/evaluation-events
exporters:
  evaluations:
    kind: sqs
    connection: queue
```

- `connection` (required): an `sqs` connection.
- The queue: `queueUrl` on the exporter, or `queueUrl` on the connection.
- `format` is accepted and ignored: the message body is always JSON.

The settings that every exporter takes (`flushInterval`, `maxEventInMemory`, `eventType`, `meta`, `overflowPolicy`, `retry` and the other pipeline settings) are in the [bootstrap file reference](../../relay-proxy/bootstrap-config.md) (section `exporters.<name>`).

## Configure the Go module

Add the following configuration to your `ffclient.Config{}` object:

```go
awsConfig, _ := config.LoadDefaultConfig(context.Background())
ffConfig := ffclient.Config{
    // ...
    DataExporter: ffclient.DataExporter{
        // ...
        Exporter: &sqsexporter.Exporter{
            QueueURL:  "https://sqs.eu-west-1.amazonaws.com/XXX/test-queue",
            AwsConfig: &awsConfig,
        },
    },
    // ...
}
client, err := ffclient.New(ffConfig)
defer client.Close()
```

| Field       | Mandatory | Description                                                                                                                               |
|-------------|:---------:|-------------------------------------------------------------------------------------------------------------------------------------------|
| `QueueURL`  | Yes       | URL of your SQS queue (available in the AWS console).                                                                                     |
| `AwsConfig` | Yes       | An instance of `*aws.Config` configuring AWS access (see [AWS SDK Go v2 docs](https://aws.github.io/aws-sdk-go-v2/docs/configuring-sdk/)). |
