# Apache Kafka

## Overview

Export evaluation data to a Kafka topic.

> [!NOTE]
> Apache Kafka is a streaming (queue-style) exporter: it sends events as soon as it receives them instead of batching them in bulk.

## Configure the relay proxy

Add the following configuration to your relay proxy configuration file:

```yaml
# ...
exporters:
  - kind: kafka
    kafka:
      topic: "semaphore-events"
      addresses:
        - "localhost:9092"
# ...
```

| Field name        | Mandatory | Type     | Default           | Description                                                                                                                                                                                                                            |
|-------------------|:---------:|----------|-------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `kind`            | Yes       | string   | **none**          | Value should be `kafka`.<br/>_This field is mandatory and describes which exporter you are using._                                                                                                                                       |
| `kafka.topic`     | Yes       | string   | **none**          | Kafka topic to publish to.                                                                                                                                                                                                             |
| `kafka.addresses` | Yes       | []string | **none**          | List of bootstrap addresses for the Kafka cluster.                                                                                                                                                                                     |
| `kafka.config`    | No        | object   | _see description_ | Optional [Sarama configuration](https://pkg.go.dev/github.com/IBM/sarama#Config) for fine-tuning the Kafka producer. When omitted, defaults from [`sarama.NewConfig()`](https://pkg.go.dev/github.com/IBM/sarama#NewConfig) are used. |

### How `kafka.config` is merged with the defaults

- Configuration starts from [`sarama.NewConfig()`](https://pkg.go.dev/github.com/IBM/sarama#NewConfig) defaults, and any non-zero value you set in `kafka.config` overrides the default (for example, `Net.DialTimeout`, `Producer.Retry.Max`, `ClientID`, or `Version`).
- Unset fields keep Sarama's defaults. Zero values (`0`, `""`, or `false`) cannot be distinguished from unset fields, so they also keep the defaults.
- `Producer.Return.Successes` and `Producer.Return.Errors` are always forced to `true` because the exporter requires them.

## Configure the Go module

Add the following configuration to your `ffclient.Config{}` object:

```go
ffConfig := ffclient.Config{
    // ...
    DataExporter: ffclient.DataExporter{
        // ...
        Exporter: &kafkaexporter.Exporter{
            Settings: kafkaexporter.Settings{
                Topic:     "semaphore-events",
                Addresses: []string{"localhost:9092", "cluster2:9092"},
            },
        },
    },
    // ...
}
client, err := ffclient.New(ffConfig)
defer client.Close()
```

| Field      | Mandatory | Description                                                                                                                                                                               |
|------------|:---------:|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Format`   | No        | Output format for Kafka messages (currently only `JSON` is supported).                                                                                                                    |
| `Settings` | Yes       | Producer configuration embedding `*sarama.Config` along with `Topic` and `Addresses`. If no `*sarama.Config` is provided, defaults from `sarama.NewConfig()` are used.                    |

`Settings` fields:

| Field       | Mandatory | Description                                    |
|-------------|:---------:|------------------------------------------------|
| `Topic`     | Yes       | Name of the topic to publish messages to.      |
| `Addresses` | Yes       | List of Kafka bootstrap server addresses.      |
