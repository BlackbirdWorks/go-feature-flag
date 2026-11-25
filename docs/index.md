---
layout: home

hero:
  name: Semaphore
  text: Self-hosted feature flags, built for speed.
  tagline: A lightweight feature flag relay proxy and embeddable Go engine with native OpenFeature OFREP support.
  image:
    src: /logo.svg
    alt: Semaphore logo
  actions:
    - theme: brand
      text: Get Started
      link: /relay-proxy/getting-started
    - theme: alt
      text: Documentation Index
      link: /overview
    - theme: alt
      text: View on GitHub
      link: https://github.com/BlackbirdWorks/semaphore

features:
  - icon: 🔌
    title: OpenFeature & OFREP Native
    details: Use standard OpenFeature SDKs in any language over HTTP using the OpenFeature Remote Evaluation Protocol (OFREP).
  - icon: ⚡
    title: Stateless Relay Proxy
    details: Single Go binary or container image with zero database dependencies, hot reloading, Flag Sets, and FIPS 140-3 builds.
  - icon: 🗄️
    title: Store Flags Anywhere
    details: Load YAML, JSON, or TOML flag files from local disk, S3, GCS, Azure Blob, GitHub, GitLab, Kubernetes ConfigMaps, Redis, PostgreSQL, or MongoDB.
  - icon: 🎯
    title: Rich Rollout Strategies
    details: Target users with query rules, consistent percentage splits, progressive ramps, scheduled rollouts, experimentation windows, and custom bucketing keys.
  - icon: 📡
    title: Export & Notify
    details: Stream evaluation and tracking events to S3, Kinesis, Kafka, Pub/Sub, BigQuery, OpenTelemetry, or webhooks, and alert Slack, Discord, or Teams on changes.
  - icon: 🧩
    title: Embeddable Go Engine
    details: Embed the core evaluation engine directly inside Go services with no network hop when you do not need a separate proxy.
---

## Quick Start

Build and run the relay proxy locally with a file retriever, or pull the container image from `ghcr.io/blackbirdworks/semaphore`:

```yaml
# flags.yaml
new-checkout:
  variations:
    enabled: true
    disabled: false
  targeting:
    - name: beta users
      query: beta eq true
      variation: enabled
  defaultRule:
    variation: disabled
```

```yaml
# semaphore-proxy.yaml
pollingInterval: 1000
retrievers:
  - kind: file
    path: ./flags.yaml
```

```shell
./out/bin/semaphore --config semaphore-proxy.yaml
```

Evaluate the flag over OFREP:

```shell
curl -s http://localhost:1031/ofrep/v1/evaluate/flags/new-checkout -X POST \
  -H 'Content-Type: application/json' \
  --data-raw '{"context": {"targetingKey": "user-123", "beta": true}}'
```

```json
{
  "key": "new-checkout",
  "value": true,
  "reason": "TARGETING_MATCH",
  "variant": "enabled",
  "metadata": {
    "evaluatedRuleName": "beta users",
    "gofeatureflag_cacheable": true,
    "semaphore_cacheable": true
  }
}
```
