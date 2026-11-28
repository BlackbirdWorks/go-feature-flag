# Apache Kafka

## Overview

Export evaluation data to a Kafka topic.

> [!NOTE]
> Apache Kafka is a streaming (queue-style) exporter: it sends events as soon as it receives them instead of batching them in bulk.

## Configure the relay proxy

Add the exporter to the `exporters` map of the [bootstrap file](../../relay-proxy/bootstrap-config.md):

```yaml
version: 2
exporters:
  evaluations:
    kind: kafka
    kafka:
      topic: semaphore-events
      addresses: ["localhost:9092"]
```

- `kafka.topic` and `kafka.addresses` (required, each address `host:port`).
- The producer settings of the Sarama configuration are keys of `kafka`, see [Kafka producer settings](../../relay-proxy/bootstrap-config.md). A zero or absent value keeps the Sarama default, and `Producer.Return.Successes` and `Producer.Return.Errors` are always `true`.
- `format` is accepted and ignored: the events are always JSON.

The settings that every exporter takes (`flushInterval`, `maxEventInMemory`, `eventType`, `meta`, `overflowPolicy`, `retry` and the other pipeline settings) are in the [bootstrap file reference](../../relay-proxy/bootstrap-config.md) (section `exporters.<name>`).

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
