# Exporter

## Overview
In Semaphore, an **exporter** is a component that sends flag evaluation results to external systems.
This allows you to track how your feature flags are being used, gather data for analysis, and monitor their impact on your application.

## About exporters
We have built the concept of **exporter** to allow you to export your evaluation data to a remote source, this can be useful in several occasion:
- **Analytics and Monitoring**: Exporters enable you to collect data on feature flag usage, such as how often a flag is evaluated, the percentage of users receiving a specific variation, and the context in which flags are evaluated. This data can be used for A/B testing, performance monitoring, and understanding user behavior.
- **Debugging and Troubleshooting**: By logging flag evaluations, exporters can help you identify issues related to feature flags, such as unexpected behavior or incorrect flag configurations.
- **Integration with External Systems**: Exporters facilitate integration with various monitoring, logging, and analytics platforms, allowing you to centralize your data and gain comprehensive insights.

## Synchronous vs Asynchronous exporters
Considering the volume of information to process and the potential impact on your application's performance, exporters can be synchronous or asynchronous depending on where the data is send.

- **Synchronous exporters**: These exporters send data immediately _(or near immediately)_ after a flag evaluation. This is used for queue system or almost 0 latency write systems.
  Exporters: Apache Kafka, AWS Kinesis, Google Cloud PubSub, AWS SQS, Webhook, Log, OpenTelemetry
- **Asynchronous exporters**: These exporters send data in batch, this is useful when you have a lot of data to send and you don't want to impact the performance of your application. This is used for all exporters writing files _(locally or remotely)_.
  Those exporters are using a buffer to store the data in memory before sending it. If for any reason the exporter can't send the data, the buffer will be used to store the data until the exporter can send it.
  Exporters: AWS S3, MinIO, Azure Blob Storage, File System, Google Cloud Storage

## Supported exporters

- AWS S3
- MinIO
- Azure Blob Storage
- File System
- Apache Kafka
- AWS Kinesis
- Google Cloud Storage
- Google Cloud PubSub
- Google Cloud BigQuery
- AWS SQS
- AWS Redshift
- Webhook
- Log
- OpenTelemetry

[Check how to configure the exporters](../integrations/exporters/README.md)

## Feature vs Tracking exporters

Exporters can be configured to receive one of two event types:

- **Feature exporters** (default): They receive **feature events**—one event per flag evaluation. Each event describes which flag was evaluated, which variation was returned, and for which user/context. Use this to understand flag usage, power analytics, and feed A/B or experimentation tools. See [flag usage tracking](../tracking/flag-usage-tracking.md) for details.
- **Tracking exporters**: They receive **tracking events**—custom actions or outcomes you record via the [OpenFeature Tracking API](https://openfeature.dev/specification/sections/tracking/) (e.g. "user completed checkout", "clicked CTA"). Use this to measure the impact of flags (e.g. conversion per variation) and run experiments. See [Tracking API](../tracking/tracking-api.md) for details.

You choose the event type per exporter with the `eventType` option (`"feature"` or `"tracking"`).
The default is `"feature"` when omitted. You can send both kinds of events to the same backend by defining two exporter entries—one for feature events, one for tracking events—if your pipeline accepts both.

For more information, see the [tracking section](../tracking/flag-usage-tracking.md) (flag usage and [Tracking API](../tracking/tracking-api.md)).

## How Exporters Work

When a feature flag is evaluated using any of the SDK, Semaphore notifies the configured exporters with the evaluation result. This result typically includes:

- The name of the flag.
- The variation returned.
- The targetingKey.
- The source of the evaluation (e.g., SERVER or PROVIDER).
- Exporter metadata (e.g., source provider, etc ...). 

The exporter then formats and sends this data to the configured destination.
