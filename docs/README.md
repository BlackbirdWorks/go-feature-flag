# Documentation

Plain-Markdown documentation for the relay proxy and the Go engine.

## Start here
- [Relay proxy: getting started](relay-proxy/getting-started.md)
- [Core concepts](concepts/architecture.md)

## Relay proxy
- [Overview](relay-proxy/README.md)
- [Installation](relay-proxy/install.md) and [Releasing](RELEASING.md)
- [Getting started](relay-proxy/getting-started.md)
- [Configuration reference](relay-proxy/configuration.md) (including flag sets and API keys)
- [Bootstrap file, version 2](relay-proxy/bootstrap-config.md) (the next configuration format; not read by the relay yet)
- [Config v2 relay core](relay-proxy/relay-core.md) (how the v2 core starts, reloads in place and shuts down; not the default binary path yet)
- [Observability](relay-proxy/observability.md) and [profiling](relay-proxy/profiling.md)
- [API endpoints](relay-proxy/endpoints.md)
- [Performance](performance.md) (throughput and latency, single and bulk evaluation, HTTP/1.1 and HTTP/2)
- [Scale](scale.md) (thousands of flag sets, reloaded in place: what is tested and how to run the full size)
- [Advanced usage](relay-proxy/advanced-usage.md) (manual refresh, flag validation, FIPS)
- Deployment: [AWS Lambda](relay-proxy/deployment/aws-lambda.md)

## Concepts
- [Architecture](concepts/architecture.md)
- [Flag evaluation](concepts/flag-evaluation.md)
- [Evaluation context](concepts/evaluation-context.md)
- [Flag sets](concepts/flagset.md)
- [Live reload](concepts/live-reload.md)
- [Flag sets and evaluation](concepts/flag-sets-and-evaluation.md)
- [Connections](concepts/connections.md)
- [Events and notifiers](concepts/events-and-notifiers.md)
- [Control-plane polling](concepts/control-plane-polling.md)
- [Control-plane file store](concepts/control-plane-file-store.md)
- [Retrievers](concepts/retriever.md), [exporters](concepts/exporter.md), [notifiers](concepts/notifier.md)

## Configure flags
- [Flag format](configure-flags/create-flags.md)
- [Targeting rules](configure-flags/target-with-flags.md)
- [Segments](configure-flags/segments.md)
- [Rollout strategies](configure-flags/rollout-strategies/README.md): [percentage](configure-flags/rollout-strategies/percentage.md), [progressive](configure-flags/rollout-strategies/progressive.md), [scheduled](configure-flags/rollout-strategies/scheduled.md), [experimentation](configure-flags/rollout-strategies/experimentation.md)
- [Custom bucketing](configure-flags/custom-bucketing.md)

## Integrations
- [Store your flags (retrievers)](integrations/retrievers/README.md)
- [Export evaluation data (exporters)](integrations/exporters/README.md)
- [Notify flag changes (notifiers)](integrations/notifiers/README.md)

## Using it from applications
- [OpenFeature SDKs with OFREP](openfeature/ofrep.md)
- [Flag usage tracking](tracking/flag-usage-tracking.md) and [tracking API](tracking/tracking-api.md)

## Go module
- [Getting started](go-module/getting-started.md)
- [Configuration](go-module/configuration.md)
- [Store flags](go-module/store_file.md), [target a user](go-module/target_user.md), [collect data](go-module/data_collection.md), [notifiers](go-module/notifier.md)
- [Control plane document format](go-module/controlplane.md) (manifest, flag-set documents and flag objects for publishers)
- [Publish to S3 (`s3publish`)](go-module/s3publish.md) (upload protocol: objects first, manifest last, conditional writes)

## Migration
- [Rename to Semaphore (old names that are still accepted)](migration/rename-to-semaphore.md)
- [Prometheus metric renames](migration/metric-renames.md)
- [Removal of `ffuser` and the `user` request field](migration/remove-ffuser.md)
- [Removal of the package-level singleton client](migration/remove-singleton.md)
- [JsonLogic behaviour changes (`jsonlogic` v3.11.0)](migration/jsonlogic-3-11.md)

## Tooling
- [Command line](tooling/cli.md): [evaluate](tooling/evaluate.md), [lint](tooling/linter.md), [generate](tooling/generate.md), [publish](tooling/publish.md), [config](tooling/config.md)
- [Auto-complete (JSON schema)](tooling/autocomplete.md)
