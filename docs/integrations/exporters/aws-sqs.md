# AWS SQS

## Overview
Export evaluation data inside a AWS SQS queue.

> [!NOTE]
> AWS SQS is an exporter of type queue, it means that it send events as soon as he receives them it does not work in bulk but in near real time.

## Configure the relay proxy

To configure your relay proxy to use the AWS SQS exporter, you need to add the following
configuration to your relay proxy configuration file:

```yaml
# ...
exporters:
  - kind: sqs
    queueUrl: "https://sqs.us-east-1.amazonaws/XXXX/test-queue"
# ...
```
| Field name |   Mandatory   | Type   | Default  | Description                                                                                           |
|------------|:-------------:|--------|----------|-------------------------------------------------------------------------------------------------------|
| `kind`     | Yes | string | **none** | **Value should be `sqs`**.<br/>_This field is mandatory and describes which retriever you are using._ |
| `queueUrl` | Yes | string | **none** | URL of your SQS queue.<br/>_You can find it in your AWS console._                                     |

## Configure the Go Module
To configure your Go module to use the AWS SQS exporter, you need to add the following
configuration to your `ffclient.Config{}` object:

```go
awsConfig, _ := config.LoadDefaultConfig(context.Background())
config := ffclient.Config{
   // ...
   DataExporter: ffclient.DataExporter{
        // ...
        Exporter: &sqsexporter.Exporter{
            QueueURL: "https://sqs.eu-west-1.amazonaws.com/XXX/test-queue",
            AwsConfig: &awsConfig,
        },
    },
    // ...
}
client, err := ffclient.New(config)
defer client.Close()
```
| Field        |   Mandatory   | Description                                                                                                                                                                                |
|--------------|:-------------:|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `QueueURL `  | Yes | URL of your SQS queue.<br/>_You can find it in your AWS console._                                                                                                                          |
| `AwsConfig ` | Yes | An instance of `aws.Config` that configures your access to AWS *(see [this documentation for more info](https://docs.aws.amazon.com/sdk-for-go/v1/developer-guide/configuring-sdk.html))*. |
