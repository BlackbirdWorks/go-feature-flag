# AWS SQS

## Overview

Export evaluation data to an AWS SQS queue.

> [!NOTE]
> AWS SQS is a streaming (queue-style) exporter: it sends events as soon as it receives them instead of batching them in bulk.

## Configure the relay proxy

Add the following configuration to your relay proxy configuration file:

```yaml
# ...
exporters:
  - kind: sqs
    queueUrl: "https://sqs.us-east-1.amazonaws/XXXX/test-queue"
# ...
```

| Field name | Mandatory | Type   | Default  | Description                                                                                      |
|------------|:---------:|--------|----------|--------------------------------------------------------------------------------------------------|
| `kind`     | Yes       | string | **none** | Value should be `sqs`.<br/>_This field is mandatory and describes which exporter you are using._ |
| `queueUrl` | Yes       | string | **none** | URL of your SQS queue (available in the AWS console).                                            |

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
