# Architecture

## Overview

Semaphore has two ways to run:

- **Relay proxy**: a stateless HTTP server used by applications via [OpenFeature SDKs](https://openfeature.dev) and [OFREP](../openfeature/ofrep.md).
- **Go module**: embed the evaluation engine directly inside a Go service with no separate proxy process.

Both share the exact same evaluation engine.

```mermaid
flowchart LR
    sources["Flag Sources\n(File, S3, Git, K8s, DB)"] --> retrievers["Retrievers\n(Polling / Refresh)"]
    retrievers --> proxy["Semaphore Relay Proxy\n(In-Memory Cache + Rules Engine)"]
    apps["Applications\n(OpenFeature SDK + OFREP)"] <-->|"HTTP / OFREP"| proxy
    proxy --> exporters["Exporters\n(Feature + Tracking Events)"]
    exporters --> sinks["Analytics & Storage\n(S3, Kafka, OTel, Webhook)"]
    proxy --> notifiers["Notifiers\n(On Flag Change)"]
    notifiers --> alerts["Alerts\n(Slack, Discord, Teams, Webhook)"]
```

## Components

### OpenFeature SDKs and OFREP

Applications use standard [OpenFeature](https://openfeature.dev) SDKs with an [OFREP](../openfeature/ofrep.md) (OpenFeature Remote Evaluation Protocol) provider talking to the relay proxy over HTTP. Semaphore does not ship language-specific providers.

### Relay Proxy

- Single stateless Go binary with no database dependency.
- Loads flag files into memory and refreshes them on a configurable interval.
- See the [Relay Proxy docs](../relay-proxy/README.md) to get started.

### Core pipelines

- **[Retrievers](retriever.md)**: fetch flag definitions from local files, object storage, Git, Kubernetes, or databases.
- **[Notifiers](notifier.md)**: alert external systems (Slack, Discord, Microsoft Teams, webhooks) when flag definitions change.
- **[Exporters](exporter.md)**: send flag evaluation and tracking events to analytics or monitoring sinks.

## Stack

- Written in Go.
- Retrievers, notifiers, and exporters use official client libraries for each backend.
