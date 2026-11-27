# Configuration

> [!WARNING]
> This page documents the version 1 configuration, which the relay proxy no longer reads: a version 1 file is rejected
> with `version 2 required`. The current format is the [bootstrap file, version 2](bootstrap-config.md). This page is
> removed with the version 1 code.

## Overview

This page documents all configuration options for the Semaphore **relay proxy**.
You can start with a minimal configuration and add options as needed.

## Configuration file

Configure the relay proxy using a configuration file (conventionally named `semaphore-proxy.yaml`, though any YAML, TOML, or JSON filename works).

Pass a custom path with `--config=/path_to_your_file.yaml`:

```shell
semaphore --config=/semaphore-proxy.yaml
```

> [!NOTE]
> When `--config` is omitted, the relay proxy searches for a configuration file in this order:
>
> 1. `./` _(current directory)_
> 2. `/semaphore/`
> 3. `/etc/opt/semaphore/`
> 4. `/goff/` _(legacy)_
> 5. `/etc/opt/goff/` _(legacy)_
>
> In each directory, it checks extensions `yaml`, `toml`, `json`, and `yml`—trying `semaphore-proxy` first, then the legacy name `goff-proxy` (used by GO Feature Flag). Using a `goff-proxy` file logs one warning at startup.

## Configuration Patterns

- [One set of feature flags](#getting-started): Single flag set for all applications (default, minimal setup).
- [Use multiple flag sets](#use-multiple-flag-sets): Separate flags into independent flag sets by team, environment, or tenant.

## Getting Started

The only required setting is at least one retriever for your flag configuration:

```yaml
retrievers:
  - kind: file
    path: /semaphore/flags.yaml # Location of your feature flag files
```

All other options are optional.

## Environment variables

You can configure or override any file setting using environment variables:

- Use the configuration option name in uppercase (for example, `logLevel` -> `LOGLEVEL`):

  ```shell
  export LOGLEVEL=debug
  ```

- Separate nested fields with `_` (for example, `server.port` -> `SERVER_PORT`):

  ```shell
  export SERVER_PORT=8080
  ```

- Separate string array items with commas:

  ```shell
  export AUTHORIZEDKEYS_EVALUATION=my-first-key,my-second-key
  ```

- Index into object arrays:

  ```shell
  export RETRIEVERS_0_KIND=github
  ```

> [!NOTE]
> The variables above have no prefix. The `SEMAPHORE_` prefix is always accepted too (`SEMAPHORE_SERVER_PORT=8080` sets `server.port`) and takes precedence when both are set.
> Note that a Kubernetes Service named `semaphore` injects variables such as `SEMAPHORE_PORT` into pods in its namespace; set `enableServiceLinks: false` on the pod if you want to avoid those variables.
>
> To avoid collisions with unrelated environment variables, set [`envVariablePrefix`](#envvariableprefix) in your configuration file.

## Configuration Options

### `server`

Configures server execution mode, host, port, and monitoring port.

- option name: `server`
- type: [`serverConfig`](#type-serverconfig)
- default: **none**
- mandatory: No
- See [`serverConfig`](#type-serverconfig) for available options.

### `retrievers`

Configures how and where to retrieve flag configuration files.

- option name: `retrievers`
- type: [`[]retriever`](#type-retriever)
- default: **none**
- **mandatory: Yes**
- Multiple retrievers can be configured together; see [_"Using multiple retrievers"_](../concepts/retriever.md#using-multiple-retrievers).
- Options depend on the retriever kind; see the [list of retrievers](../integrations/retrievers/README.md#available-retrievers).

```yaml
retrievers:
  - kind: file
    path: /semaphore/my-flags.yaml
  - kind: s3
    bucket: my-featureflag-bucket
    item: flag/flags.yaml
```

### `startWithRetrieverError`

By default, the relay proxy crashes if it fails to retrieve flags at startup. Set `startWithRetrieverError` to `true` to start anyway and return default values until flags become retrievable.

- option name: `startWithRetrieverError`
- type: **boolean**
- default: **`false`**
- mandatory: No

### `persistentFlagConfigurationFile`

Local file path used to persist flag configurations so Semaphore can serve flags at startup even if all retrievers are unavailable.

> [!NOTE]
> By default, flag configuration is not persisted locally. Setting a persistence file lets Semaphore start with cached (possibly outdated) flags when retrievers are unreachable.

- option name: `persistentFlagConfigurationFile`
- type: **string**
- default: **none**
- mandatory: No
- example: `/tmp/semaphore_persist_conf.yaml`

### `fileFormat`

Format of your flag configuration file.

- option name: `fileFormat`
- type: **string**
- Acceptable values: `yaml`, `json`, `toml`.
- default: **`yaml`**
- mandatory: No

### `pollingInterval`

Interval **in milliseconds** between configuration reloads. Also used by shared pollers for flag sets with [`enableSharedPollerPerBucket`](#flagsetenablesharedpollerperbucket).

- option name: `pollingInterval`
- type: **int**
- default: **`60000`**
- Minimum accepted value: `1000` milliseconds (1 second).
- Negative values disable polling.
- mandatory: No

### `enablePollingJitter`

Adds random jitter to polling intervals to prevent traffic spikes on flag storage when multiple instances start simultaneously. Also used by shared pollers for flag sets with [`enableSharedPollerPerBucket`](#flagsetenablesharedpollerperbucket).

> [!NOTE]
> Deviation is at most ±10% of `pollingInterval`.

- option name: `enablePollingJitter`
- type: **boolean**
- default: **`false`**
- mandatory: No

### `logLevel`

Log level for the relay proxy (see [Logging](observability.md#logging)).

- option name: `logLevel`
- type: **string**
- default: **`info`**
- Acceptable values: `error`, `warn`, `info`, `debug`.
- mandatory: No
- In `debug` mode, the relay proxy logs request and response bodies (truncated beyond **8KiB** to limit performance impact), enables debug HTTP error responses, and enables [profiling endpoints](profiling.md). To enable profiling without debug logging, use [`enablePprof`](#enablepprof).

### `logFormat`

Format for structured logs from the relay proxy (see [Logging](observability.md#logging)).

- option name: `logFormat`
- type: **string**
- default: **`json`**
- Acceptable values: `json`, `logfmt`.
- mandatory: No

### `enablePprof`

Enables **pprof** profiling endpoints (see [Profiling](profiling.md)).

- option name: `enablePprof`
- type: **boolean**
- default: **`false`**
- mandatory: No

### `exporters`

Configures where evaluation and tracking events are exported.

- option name: `exporters`
- type: **[[]exporter](#type-exporter)**
- default: **none**
- mandatory: No
- Multiple exporters run in parallel.
- Options depend on the exporter kind; see the [list of exporters](../integrations/exporters/README.md#available-exporters).

```yaml
exporters:
  - kind: s3
    bucket: evaluation-data-bucket
  - kind: kafka
    kafka:
      topic: "semaphore-events"
```

### `notifiers`

Configures where flag change notifications are sent.

- option name: `notifiers`
- type: **[[]notifier](#type-notifier)**
- default: **none**
- mandatory: No
- Options depend on the notifier kind; see the [list of notifiers](../integrations/notifiers/README.md#available-notifiers).

### `disableNotifierOnInit`

When `true`, skips calling notifiers on the initial flag load at startup (avoids notifications on every proxy restart).

- option name: `disableNotifierOnInit`
- type: **boolean**
- default: **`false`**
- mandatory: No

### `exporterCleanQueueInterval`

Duration between cleanups of the exporters' event queue; cleanup removes events already processed by every exporter. In [flag set mode](#use-multiple-flag-sets), this applies to flag sets that do not set [`flagSet.exporterCleanQueueInterval`](#flagsetexportercleanqueueinterval).

- option name: `exporterCleanQueueInterval`
- type: **duration** _(for example `30s` or `2m`)_
- default: **`1m`**
- mandatory: No
- Negative values are rejected when loading configuration.

### `hideBanner`

Hides the startup banner when `true`.

- option name: `hideBanner`
- type: **boolean**
- default: **`false`**
- mandatory: No

### `swagger`

Configures Swagger UI behavior for the relay proxy.

- option name: `swagger`
- type: **[swagger](#type-swagger)**
- default: **none**
- mandatory: No

### `ofrepEventStream`

Advertises the flag-change SSE endpoint in the OFREP bulk evaluation response so that OFREP providers can subscribe to changes instead of polling (see [OpenFeature ADR-0008](https://github.com/open-feature/protocol/blob/main/service/adrs/0008-sse-for-bulk-evaluation-changes.md)).

It is enabled when [`ofrepEventStream.baseUrl`](#ofrepeventstreambaseurl) is set.

- option name: `ofrepEventStream`
- type: **[ofrepEventStream](#type-ofrepeventstream)**
- default: **none**
- mandatory: No

### `disableVersionHeader`

If `disableVersionHeader` is set to **`true`**, the relay proxy will not add the headers `x-semaphore-version` and its legacy name `x-gofeatureflag-version` with the Semaphore version in the HTTP response.

- option name: `disableVersionHeader`
- type: **boolean**
- default: **`false`**
- mandatory: No

### `disableFlagDetailsInStream`

If `disableFlagDetailsInStream` is set to **`true`**, WebSocket flag change payloads omit flag definitions, including targeting rules. The `deleted`, `added`, and `updated` objects retain the changed flag names with empty object values so clients can refresh their evaluated flags without receiving the underlying configuration.
Enabling this setting requires restarting the relay proxy.

- option name: `disableFlagDetailsInStream`
- type: **boolean**
- default: **`false`**
- mandatory: No

### `endpoints`

Configuration of the endpoints of the relay proxy.

#### `endpoints.validateFlags`

Body size limits of the [`POST /admin/v1/flags/validate`](advanced-usage.md#validate-a-flag-file) endpoint, one per format.
A request with a larger body is rejected with a `413`.

Each value is either a number of bytes (`1048576`) or a human-readable size (`1MiB`, `64KiB`, `2MB`).
An unset (or `0`) value keeps the default, a negative value is rejected when the configuration is loaded.
SI units are decimal (`2MB` = 2,000,000 bytes) while IEC units are binary (`2MiB` = 2,097,152 bytes).
Changing this setting requires restarting the relay proxy.

| Option            | Type                | Default            |
|-------------------|---------------------|--------------------|
| `maxJSONBodySize` | integer or string | `1048576` (1 MiB)  |
| `maxYAMLBodySize` | integer or string | `65536` (64 KiB)   |
| `maxTOMLBodySize` | integer or string | `65536` (64 KiB)   |

```yaml
endpoints:
  validateFlags:
    maxJSONBodySize: 1048576 # 1 MiB
    maxYAMLBodySize: 64KiB
    maxTOMLBodySize: 64KiB
```

The environment variables are `ENDPOINTS_VALIDATEFLAGS_MAXJSONBODYSIZE`, `ENDPOINTS_VALIDATEFLAGS_MAXYAMLBODYSIZE`
and `ENDPOINTS_VALIDATEFLAGS_MAXTOMLBODYSIZE` (for example `ENDPOINTS_VALIDATEFLAGS_MAXYAMLBODYSIZE=128KiB`).

> [!WARNING]
> The YAML and TOML limits are lower than the JSON one on purpose: the parsing cost of those formats grows
> quadratically with the size of the body (tracked in [#6439](https://github.com/BlackbirdWorks/semaphore/issues/6439)).
> Raising them exposes the relay proxy to slower requests, so keep them as small as your flag files allow.

- option name: `endpoints`
- type: **object**
- default: **none**
- mandatory: No

### `authorizedKeys`

List of authorized API keys.

> [!NOTE]
> If you don't set any API keys, the relay proxy will be fully open to all requests

- option name: `authorizedKeys`
- type: **[authorizedKeys](#type-authorizedkeys)**
- default: **none**
- mandatory: No

> [!TIP]
> **Runtime updates**: API keys can be updated at runtime without restarting the relay proxy.
> When the configuration file changes, the relay proxy automatically detects and applies the new API key configuration in both default mode and flag set mode (`authorizedKeys.admin` and each flag set's [`apiKeys`](#flagsetapikeys) are reloaded).
> `authorizedKeys` are swapped atomically: each request checks against either the old or new keys.
> In flag set mode, a flag set key routes to its flag set at authentication and stays pinned to it for the request: a key moved or removed during reload is either served by one of its flag sets or rejected with `401`, never failing halfway. The only exception is `/v1/data/collector`, which re-resolves its flag set after reading the body so events are never acknowledged to a closed flag set.

### `evaluationContextEnrichment`

Arbitrary key-value map merged into the custom attributes of every evaluation context (useful for common metadata like server version or environment).

> [!WARNING]
> Keys in `evaluationContextEnrichment` override fields with the same name in the incoming evaluation context.

- option name: `evaluationContextEnrichment`
- type: **object**
- default: **none**
- mandatory: No

### `openTelemetryOtlpEndpoint`

Endpoint of your **OpenTelemetry OTLP collector** for exporting traces.

- option name: `openTelemetryOtlpEndpoint`
- type: **string**
- default: **none**
- mandatory: No

### `flagSets`

Flag sets allow you to organize your feature flags into separate groups, each with its own configuration, API keys, retrievers, exporters, and notifiers.

When you configure flag sets, the relay proxy operates in **flag set mode** instead of the default single configuration mode. Each flag set requires at least one API key and can have its own retrievers, exporters, and notifiers.

- option name: `flagSets`
- type: **[[]flagSet](#type-flagset)**
- default: **none**
- mandatory: No
- If flag sets are configured, the relay proxy will ignore the top-level `retrievers`, `exporters`, and `notifiers` configuration.
- Cannot be used together with [`studio`](#studio).

```yaml
server:
  mode: http
  port: 1031
flagSets:
  - name: teamA
    apiKeys:
      - "team-a-api-key"
    retrievers:
      - kind: file
        path: /flags/team-a-flags.yaml
    exporters:
      - kind: log

  - name: teamB
    apiKeys:
      - "team-b-api-key"
    retrievers:
      - kind: s3
        bucket: my-feature-flag-bucket
        item: team-b/flags.yaml
    exporters:
      - kind: s3
        bucket: evaluation-data-bucket
```

**Runtime updates**: flag sets can be **added** or **removed** at runtime without restarting the relay proxy.  
When the configuration file changes, the relay proxy detects it and reconciles the running flag sets:
a newly added flag set is started immediately and a removed flag set is stopped.

> [!WARNING]
> - **Modifying** an existing flag set (its `retrievers`, `exporters`, `notifiers`, `pollingInterval`, …) is **not supported at runtime**. The only exception is its `apiKeys`, which can be changed live. Any other modification is **ignored** and an error is logged; the change will only be applied the next time the relay proxy is **restarted**.
> - Runtime add/remove only works for flag sets that have an explicit, unique [`name`](#flagsetname). Flag sets without a name (or named `default`) get a generated name at startup and cannot be added or removed at runtime.

#### Sharing S3 requests between flag sets

By default every flag set polls its own retriever, so `N` flag sets reading `N` files from the same S3 bucket send `N` `GetObject` requests per polling interval and per relay proxy replica.

With [`enableSharedPollerPerBucket`](#flagsetenablesharedpollerperbucket) set to **true**, the flag sets of the same bucket share **one poller**.
Every polling interval the poller does **one recursive listing** of the bucket, compares the ETag of each file with the one it already holds, and refreshes **only the flag sets whose file changed**, each of them downloading just its own file.

```yaml
pollingInterval: 5000 # used by the shared poller of every bucket
flagSets:
  - name: project-1-prod # no option: keeps its own retriever and its own polling
    apiKeys: ["prod-key"]
    retriever:
      kind: s3
      bucket: flags
      item: flags/project-1/prod/flags.yaml

  - name: project-1-dev
    enableSharedPollerPerBucket: true
    apiKeys: ["dev-key"]
    retriever:
      kind: s3
      bucket: flags
      item: flags/project-1/dev/flags.yaml

  - name: project-2-dev
    enableSharedPollerPerBucket: true
    apiKeys: ["dev-2-key"]
    retriever:
      kind: s3
      bucket: flags
      item: flags/project-2/dev/flags.yaml
```

- Only the flag sets with the same `bucket` and `enableSharedPollerPerBucket: true` are grouped, the others behave as usual. Keep your production flag sets isolated by not setting the option on them.
- **Polling**: the shared poller uses the top-level `pollingInterval` and `enablePollingJitter` (60s by default, 1s at least). If an opted-in flag set sets a different `pollingInterval` or `enablePollingJitter`, a **warning** naming the flag set and both values is logged and the top-level values are used. The configuration is not rejected.
- **Layout**: put the flag files under a dedicated prefix (like `flags/` above). The poller lists the longest common key prefix of the items, cut at its last `/`, and the listing covers **every object under it**. Items with no common directory list **the whole bucket**.
- **IAM**: in addition to `s3:GetObject`, the relay proxy needs `s3:ListBucket` on the bucket for the listing prefix. A denied prefix fails the listing for **the whole group**.

##### Behaviour

- **Detection delay**: a change is detected at the next listing, so within one `pollingInterval` like with an independent retriever.
- **Startup**: each flag set downloads its file once when it starts and is ready after that. Downloads are limited to 16 concurrent `GetObject` requests per bucket.
- **Errors**: a failed refresh of one flag set does not stop the others and is retried at the next poll. A failed listing is logged and retried at the next polling interval, the flag sets keep serving their last known flags meanwhile. Each listing page request has a 30 second timeout and a listing is limited to 10000 pages.
- **Reload**: a new top-level `pollingInterval` or `enablePollingJitter` is applied to the running pollers without rebuilding the flag sets. Like the other flag set options, `enableSharedPollerPerBucket` can't be changed on an existing flag set at runtime.
- **Lifecycle**: the poller of a bucket starts with its first flag set and stops when the last one is removed or the relay proxy shuts down.
- The `timeout` of the `retriever` is not used by this path.

##### Request load on the bucket

With `N` flag sets, `M` objects under the prefix and `C` changed objects per interval, independent retrievers send `N` `GetObject` requests per interval and per replica, and the shared poller sends `ceil(M / 1000)` listing pages plus `C` `GetObject` requests. The request count goes down while `M` is below roughly `1000 * (N - C)`. Keep the prefix dedicated to the flag files, and leave the option off for a handful of flag sets.
In dollars, a listing page costs about 12.5 times a `GetObject`, so the cost only goes down while `M` is below about `80 * N`: the cost is secondary, the goal is the request load.

Measured on this implementation (one poller per bucket, each flag set keeping its own client with polling disabled):

Per pod, 60s window, 5s polling, 1% of flagsets rewritten every polling interval, single run each, local in-memory S3 emulator:

| Flagsets x flags | Metric | Current | SharedRetriever | Delta |
|---|---|---|---|---|
| 100 x 10 | S3 requests | 1,200 GET | 12 LIST + 12 GET | -1,176 (50x fewer) |
| | RSS (MiB) | 83.0 | 74.4 | -8.5 (-10%) |
| | Heap in use (MiB) | 23.3 | 17.1 | -6.2 (-27%) |
| | Goroutines | 318 | 21 | -297 (15x fewer) |
| | Allocations (count / bytes) | 5.20M / 331 MiB | 0.19M / 10.8 MiB | 27x / 31x fewer |
| 100 x 20 | S3 requests | 1,200 GET | 12 LIST + 12 GET | -1,176 (50x fewer) |
| | RSS (MiB) | 90.1 | 80.8 | -9.3 (-10%) |
| | Heap in use (MiB) | 27.1 | 21.2 | -5.9 (-22%) |
| | Goroutines | 317 | 22 | -295 (14x fewer) |
| | Allocations (count / bytes) | 9.72M / 583 MiB | 0.31M / 19.0 MiB | 31x / 31x fewer |
| 200 x 10 | S3 requests | 2,400 GET | 12 LIST + 24 GET | -2,364 (67x fewer) |
| | RSS (MiB) | 96.3 | 71.5 | -24.8 (-26%) |
| | Heap in use (MiB) | 37.7 | 25.9 | -11.8 (-31%) |
| | Goroutines | 617 | 22 | -595 (28x fewer) |
| | Allocations (count / bytes) | 10.38M / 662 MiB | 0.37M / 21.0 MiB | 28x / 32x fewer |
| 200 x 20 | S3 requests | 2,400 GET | 12 LIST + 24 GET | -2,364 (67x fewer) |
| | RSS (MiB) | 107.8 | 95.9 | -12.0 (-11%) |
| | Heap in use (MiB) | 36.4 | 29.4 | -7.0 (-19%) |
| | Goroutines | 617 | 22 | -595 (28x fewer) |
| | Allocations (count / bytes) | 19.39M / 1,166 MiB | 0.59M / 36.6 MiB | 33x / 32x fewer |

Measurements: one pod, one run per cell, in-memory S3 emulator on a single host; memory figures are single end-of-window samples and vary by several MiB between runs. Transferred bytes are not reported because the counting proxy did not capture LIST response sizes. The request reduction depends on the polling interval and on how many objects change: at the production default of 60s with the same write rate it is roughly N / (1 + changed objects per interval), about 8x here, not 50-67x. Multi-replica totals scale linearly with the number of pods for requests (each pod polls independently) but were not measured.

- **Caveats**: each replica still downloads each object once when it starts. The listing covers every object under the common prefix, so unrelated objects raise the number of listing pages. `s3:ListBucket` must be allowed on the prefix.

### `studio`

Loads the [`flagSets`](#flagsets) from a remote configuration service of your own instead of the configuration file.
This is opt-in: no URL is configured by default, nothing is fetched unless `studio.url` is set, and Semaphore does not
provide or default to any hosted service. The only host contacted is the one you configure.

When `studio.url` is set, the relay proxy fetches the document served at this URL and only uses its `flagSets` key.
The rest of the configuration (`server`, `authorizedKeys`, …) still comes from the configuration file and the environment variables.

- option name: `studio`
- type: [`studio`](#type-studio)
- default: **none**
- mandatory: No
- `studio` and `flagSets` cannot be used together, the relay proxy refuses to start if both are set.
- Check the [studio configuration type](#type-studio) to have more information about the available options.

```yaml
server:
  mode: http
  port: 1031
studio:
  url: https://studio.example.com/relay-proxy/semaphore-proxy.yaml
  headers:
    Authorization:
      - "Bearer my-token"
  pollingInterval: 60000
  cacheFile: /semaphore/studio-cache.yaml
```

**Runtime updates**: the URL is polled every [`studio.pollingInterval`](#studiopollinginterval) milliseconds.
When the `flagSets` change, they are applied the same way as a change of the [`flagSets`](#flagsets) in the configuration file.
A document with invalid `flagSets` is rejected, an error is logged and the relay proxy keeps the previous flag sets.

> [!WARNING]
> Changes to the `studio` configuration itself (`url`, `headers`, `pollingInterval`, `cacheFile`) require restarting the relay proxy.

### `envVariablePrefix`

Prefix for your environment variables, useful to avoid collisions with unrelated environment variables.

When set (for example, `MYAPP_`), prefix your environment variables with it:

```shell
export MYAPP_SERVER_PORT=8080
```

The `SEMAPHORE_` prefix is always read in addition to this prefix and wins when both are set. A prefix starting with `GOFF` (`GOFF_`, `GOFFPROXY_`, used with GO Feature Flag) still works and logs one warning at startup.

Kubernetes injects Service variables into pods (`<SERVICE>_SERVICE_HOST`, `<SERVICE>_SERVICE_PORT`, `<SERVICE>_PORT=tcp://...`, `<SERVICE>_PORT_<port>_TCP_ADDR`, ...), so a Service named `semaphore` adds `SEMAPHORE_*` variables. The relay proxy ignores variables that match both the name and value shape of a service link (logging only their names at `debug` level); settings like `SEMAPHORE_SERVER_PORT=1031` are unaffected. Set `enableServiceLinks: false` on your pods to omit them.

- option name: `envVariablePrefix`
- type: **string**
- default: **empty string**
- mandatory: No

### `exposeLegacyMetrics`

The Prometheus series of the relay proxy are named `semaphore_*`. If `exposeLegacyMetrics` is set to **`true`**, every
`semaphore_*` series is also exposed with the legacy prefix of GO Feature Flag (`gofeatureflag_*`, for example
`gofeatureflag_flag_evaluations_total`), so the dashboards and the alerts written before the rename keep working while
you migrate them. The Go runtime and process metrics are not duplicated.

- option name: `exposeLegacyMetrics`
- type: **boolean**
- default: **`false`**
- mandatory: No

### `disableSemaphoreMetadata`

The OFREP evaluation responses carry the metadata key `semaphore_cacheable` and its legacy name `gofeatureflag_cacheable`
when the value can be cached by the client. If `disableSemaphoreMetadata` is set to **`true`**, only the legacy key is
sent. Use it for the clients that compare the metadata of the response strictly, such as the test suites of the GO
Feature Flag providers.

- option name: `disableSemaphoreMetadata`
- type: **boolean**
- default: **`false`**
- mandatory: No

### `enableBulkMetricFlagNames`

Enables per-flag metrics for bulk evaluation endpoints.
This adds `flag_name` labels to the `all_flags_evaluations_with_flag_total` metric.

> [!NOTE]
> When enabled, this option provides more granular metrics by including the flag name in the metric labels for bulk evaluation endpoints. This can be useful for monitoring and alerting on specific flags, but may increase the cardinality of your metrics.

- option name: `enableBulkMetricFlagNames`
- type: **boolean**
- default: **`false`**
- mandatory: No

## Configuration Types

### type `authorizedKeys`

Configures the API keys accepted by the relay proxy.

#### `authorizedKeys.evaluation`

Restricts evaluation endpoints to requests carrying one of these keys in the `X-API-Key` header.

> [!WARNING]
> In [flag set mode](#use-multiple-flag-sets), this option is **ignored**: requests route to a flag set by API key, so only the [`apiKeys` of each flag set](#flagsetapikeys) can evaluate flags.
> A key listed only here returns `401` on evaluation endpoints, and logs a warning at startup and on each reload.

- option name: `evaluation`
- type: **[]string**
- default: **none**
- mandatory: No

#### `authorizedKeys.admin`

API keys required in the `X-API-Key` header to access `/admin/v1/*` endpoints.

- option name: `admin`
- type: **[]string**
- default: **none**
- mandatory: No
- If no API key is configured, `/admin/v1/*` endpoints are unreachable.

#### Example

```yaml
# ...
authorizedKeys:
  evaluation:
    - "my-first-key"
    - "my-second-key"
  admin:
    - "my-first-admin-key"
    - "my-second-admin-key"
```

### type `serverConfig`

Configures server behavior of the relay proxy.

#### `server.mode`

Execution mode of the relay proxy.

- option name: `mode`
- type: **string**
- Acceptable values: `http`, `lambda`, `unixsocket`.
- default: **http**
- mandatory: No
- `http`: Start the relay proxy as an HTTP server.
  `lambda`: Start the relay proxy as an AWS Lambda handler.
  `unixsocket`: Listen on a Unix socket (requires `unixSocketPath`).

#### `server.unixSocketPath`

The path where the Unix socket file will be created when the relay proxy is started in `unixsocket` mode.
If used in combination with `server.monitoringPort` monitoring endpoints will be served via http on the provided port.

- option name: `unixSocketPath`
- type: **string**
- default: **none**
- mandatory: No
- This field is mandatory if the `mode` is set to `unixsocket`.

#### `server.host`

The server host used by the relay proxy to bind the HTTP server.

- option name: `host`
- type: **string**
- default: **`0.0.0.0`**
- mandatory: No
- This field is used only in `http` mode.

#### `server.port`

The server port used by the relay proxy to bind the HTTP server.

- option name: `port`
- type: **int**
- default: **1031**
- mandatory: No
- This field is used only in `http` mode.

#### `server.enableH2C`

Accept HTTP/2 over cleartext TCP (h2c with prior knowledge) on the API port, next to HTTP/1.1. Clients that
do not speak h2c keep working. HTTP/2 multiplexes many requests on one connection, which matters mostly for clients
that open few connections; see the [performance page](../performance.md) for what it changed in our measurements.
Behind a TLS terminating proxy or load balancer the HTTP/2 hop is usually handled there, so you rarely need it.

- option name: `enableH2C`
- type: **bool**
- default: **false**
- mandatory: No
- This field is used only in `http` mode. Environment variable: `SERVER_ENABLEH2C`.

#### `server.monitoringPort`

If set the monitoring endpoints will be served on this specific port.

See [_"Use a dedicated monitoring port"_](observability.md#use-a-dedicated-monitoring-port) for more information.

- option name: `monitoringPort`
- type: **int**
- default: **none**
- mandatory: No
- This field is used only in `http` mode.

#### `server.awsLambdaAdapter`

Selects which AWS Lambda event adapter to use.

- option name: `awsLambdaAdapter`
- type: **string**
- default: **`APIGatewayV2`**
- Acceptable values are `APIGatewayV2`, `APIGatewayV1`, `ALB`.
- mandatory: No
- This param is used only if `server.mode` is set to `lambda`.

#### `server.awsApiGatewayBasePath`

Specifies the base path prefix for AWS API Gateway deployments when using non-root routes.
The relay proxy will strip this base path from incoming requests before processing them.
This is useful when deploying behind paths like `/api` or `/dev/feature-flags`.

- option name: `awsApiGatewayBasePath`
- type: **string**
- default: **none**
- mandatory: No
- This param is used only if `server.mode` is set to `lambda`.

**Example:** If set to `/api/feature-flags`, requests to `/api/feature-flags/health` will be processed as `/health`.

### type `retriever`

A [retriever](../concepts/retriever.md) loads your flag configuration from a local or remote source.

#### `retriever.kind`

Selects which retriever to use (the only field common to all retrievers).

- option name: `kind`
- type: **string**
- default: **none**
- mandatory: Yes

> [!NOTE]
> See the [list of retrievers](../integrations/retrievers/README.md#available-retrievers) and open the corresponding page for each retriever's options.
>
> **Available retrievers**:
>
> - [HTTP(S)](../integrations/retrievers/http.md)
> - [File System](../integrations/retrievers/file.md)
> - [Kubernetes ConfigMap](../integrations/retrievers/kubernetes-configmap.md)
> - [AWS S3](../integrations/retrievers/aws-s3.md)
> - [MinIO](../integrations/retrievers/minio.md)
> - [Google Cloud Storage](../integrations/retrievers/google-cloud-storage.md)
> - [Azure Blob Storage](../integrations/retrievers/azure-blob-storage.md)
> - [GitHub](../integrations/retrievers/github.md)
> - [GitLab](../integrations/retrievers/gitlab.md)
> - [Bitbucket](../integrations/retrievers/bitbucket.md)
> - [MongoDB](../integrations/retrievers/mongodb.md)
> - [Redis](../integrations/retrievers/redis.md)
> - [PostgreSQL](../integrations/retrievers/postgresql.md)

### type `exporter`

An [exporter](../concepts/exporter.md) sends evaluation and tracking events to a destination.

```yaml
# ...
exporters:
  - kind: s3
    bucket: evaluation-data-bucket
    flushInterval: 10000
```

> [!NOTE]
> See the [list of exporters](../integrations/exporters/README.md#available-exporters) and open the corresponding page for each exporter's options.
>
> **Available exporters**:
>
> - [AWS S3](../integrations/exporters/aws-s3.md)
> - [MinIO](../integrations/exporters/minio.md)
> - [Azure Blob Storage](../integrations/exporters/azure-blob-storage.md)
> - [File System](../integrations/exporters/file.md)
> - [Apache Kafka](../integrations/exporters/kafka.md)
> - [AWS Kinesis](../integrations/exporters/aws-kinesis.md)
> - [Google Cloud Storage](../integrations/exporters/google-cloud-storage.md)
> - [Google Cloud PubSub](../integrations/exporters/google-cloud-pubsub.md)
> - [Google Cloud BigQuery](../integrations/exporters/google-cloud-bigquery.md)
> - [AWS SQS](../integrations/exporters/aws-sqs.md)
> - [AWS Redshift](../integrations/exporters/aws-redshift.md)
> - [Webhook](../integrations/exporters/webhook.md)
> - [Log](../integrations/exporters/log.md)
> - [OpenTelemetry](../integrations/exporters/opentelemetry.md)

### type `flagSet`

A flag set is a collection of flags that are used to evaluate features. It is used to group flags together and to apply the same configuration to them.

```yaml
flagSets:
  - name: my-flagset
    apiKeys:
      - "my-api-key"
    retrievers:
      - kind: file
        path: /flags/my-flags.yaml
    exporters:
      - kind: s3
        bucket: my-evaluations
    notifiers:
      - kind: slack
        webhookUrl: "https://hooks.slack.com/services/..."
```

#### `flagSet.name`

The name of the flag set. This allows you to identify the flag set.

- option name: `name`
- type: **string**
- default: **auto-generated UUID**
- mandatory: No

> [!WARNING]
> If you want to **add/remove the flag set** or **update its API keys** at runtime without restarting the relay proxy, you **must provide a `name`** for your flag set.  
> Without a name (or using `default` as the name), a UUID is generated and the flag set cannot be managed at runtime.

#### `flagSet.apiKeys`

The API keys for the flag set. This will add new API keys to the list of authorized keys for evaluation. This property is mandatory for every flag set, as it is used to filter the flags available.

- option name: `apiKeys`
- type: **[]string**
- default: **none**
- mandatory: Yes

#### `flagSet.retrievers`

Configuration for retrieving flag configurations. Supports the same options as the main configuration.

- [_see `retrievers`_](#retrievers)

#### `flagSet.exporters`

Configuration for exporting evaluation data. Supports the same options as the main configuration.

- [_see `exporters`_](#exporters)

#### `flagSet.notifiers`

Configuration for flag change notifications. Supports the same options as the main configuration.

- [_see `notifiers`_](#notifiers)

#### `flagSet.fileFormat`

The format of the flag configuration files (yaml, json, toml).

- [_see `fileFormat`_](#fileformat)

#### `flagSet.pollingInterval`

The time interval in milliseconds when the relay proxy is reloading the configuration file.

- [_see `pollingInterval`_](#pollinginterval)

#### `flagSet.startWithRetrieverError`

If true, the flag set will start even if the retriever fails initially.

- [_see `startWithRetrieverError`_](#startwithretrievererror)

#### `flagSet.enablePollingJitter`

Set to true if you want to avoid having true periodicity when retrieving your flags.

- [_see `enablePollingJitter`_](#enablepollingjitter)

#### `flagSet.disableNotifierOnInit`

Set to true if you do not want to call any notifier when the flags are loaded for the first time.

- [_see `disableNotifierOnInit`_](#disablenotifieroninit)

#### `flagSet.exporterCleanQueueInterval`

Duration between two cleanings of the exporters' event queue for this flag set.

- type: **duration**
- default: the top-level [`exporterCleanQueueInterval`](#exportercleanqueueinterval), else **`1m`**
- [_see `exporterCleanQueueInterval`_](#exportercleanqueueinterval)

#### `flagSet.evaluationContextEnrichment`

Additional context data to merge with all evaluation requests for this flag set.

- [_see `evaluationContextEnrichment`_](#evaluationcontextenrichment)

#### `flagSet.persistentFlagConfigurationFile`

File path to persist flag configurations for this flag set.

- [_see `persistentFlagConfigurationFile`_](#persistentflagconfigurationfile)

#### `flagSet.environment`

Environment identifier for this flag set (e.g., "dev", "staging", "prod").

- [_see `environment`_](#flagsetenvironment)

#### `flagSet.enableSharedPollerPerBucket`

If `enableSharedPollerPerBucket` is set to **true**, the flag set does not poll its own retriever: it shares one poller per bucket with the other flag sets of the same S3 bucket that also set this option. Each flag set keeps its own client (with its own polling turned off), caches, notifiers and exporters.  
The shared poller uses the top-level [`pollingInterval`](#pollinginterval) and [`enablePollingJitter`](#enablepollingjitter). If the flag set sets different values, the relay proxy logs a warning and the top-level values win.  
It requires a single `retriever` of kind `s3` with `item` set, and it is only available inside a flag set.

See [Sharing S3 requests between flag sets](#sharing-s3-requests-between-flag-sets) for the details.

- option name: `enableSharedPollerPerBucket`
- type: **boolean**
- default: **false**
- mandatory: No

### type `notifier`

A [notifier](../concepts/notifier.md) sends notifications to a remote system when flags change.

```yaml
# ...
notifiers:
  - kind: discord
    webhookUrl: "https://discord.com/api/webhooks/yyyy/xxxxxxx"
  - kind: slack
    webhookUrl: "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX"
# ...
```

> [!NOTE]
> See the [list of notifiers](../integrations/notifiers/README.md#available-notifiers) and open the corresponding page for each notifier's options.
>
> **Available notifiers**:
> - [Slack](../integrations/notifiers/slack.md)
> - [Discord](../integrations/notifiers/discord.md)
> - [Microsoft Teams](../integrations/notifiers/microsoft-teams.md)
> - [Webhook](../integrations/notifiers/webhook.md)
> - Log

### type `studio`

Configuration used to load flag sets from a remote Studio configuration service.

#### `studio.url`

URL of the relay proxy configuration served by Studio; only its `flagSets` key is used.

Format is detected from the URL path extension (`.yaml`, `.json`, or `.toml`), ignoring query strings so pre-signed URLs work. Without a known extension, YAML is assumed.

- option name: `url`
- type: **string**
- default: **none**
- mandatory: No _(Studio is enabled only when set)_

#### `studio.headers`

Headers sent with every request to [`studio.url`](#studiourl), for example an `Authorization` header.

- option name: `headers`
- type: **map[string][]string**
- default: **none**
- mandatory: No

```yaml
studio:
  url: https://studio.example.com/relay-proxy/semaphore-proxy.yaml
  headers:
    Authorization:
      - "Bearer my-token"
```

#### `studio.pollingInterval`

Time **in milliseconds** between two checks of [`studio.url`](#studiourl).

- option name: `pollingInterval`
- type: **int**
- default: **`60000`**
- A value of `0` or a negative value uses the default.
- mandatory: No

#### `studio.cacheFile`

Path of a local file where the relay proxy keeps the last valid document received from Studio.
When [`studio.url`](#studiourl) is unreachable, the flag sets are loaded from this file and a warning is logged.

- option name: `cacheFile`
- type: **string**
- default: **none** _(no cache)_
- mandatory: No
- example: `/semaphore/studio-cache.yaml`

### type `swagger`

Configures Swagger UI behavior for the relay proxy.

#### `swagger.enabled`

Enables Swagger UI at `http://<host>:<server.port>/swagger/`. Swagger UI sends requests to the origin it is served from (even behind an ingress), so [`swagger.host`](#swaggerhost) is no longer needed.

- option name: `enabled`
- type: **boolean**
- default: **`false`**
- mandatory: No

#### `swagger.host`

Unused: Swagger UI calls the origin of its own page. Kept for configuration backward compatibility.

- option name: `host`
- type: **string**
- default: **`localhost`**
- mandatory: No

### type `ofrepEventStream`

Configures advertising the flag-change SSE endpoint in the OFREP bulk evaluation response.

When [`ofrepEventStream.baseUrl`](#ofrepeventstreambaseurl) is set, bulk evaluation responses include an `eventStreams` field pointing to the SSE endpoint (`/stream/v1/sse/flag/change`).

Whenever flag configuration changes, this endpoint emits an ADR-0008 event instructing the provider to re-fetch evaluations:

```
id: 1771622898000000000
event: message
data: {"type":"refetchEvaluation","lastModified":1771622898}
```

The stream also sends SSE comment lines ignored by spec-compliant clients: `: connected` on open, and `: heartbeat` every 30 seconds to keep idle connections alive through proxies.

If the OFREP request is authenticated, the caller's API key is appended as the `apiKey` query parameter in the advertised URL so the provider can subscribe without extra credentials. Per the ADR, this URL is sensitive and must not be logged or persisted.

#### `ofrepEventStream.baseUrl`

Public base URL clients use to reach the relay proxy (for example `https://semaphore.example.com`). The SSE endpoint path is appended automatically while preserving any path prefix (`https://example.com/semaphore` becomes `https://example.com/semaphore/stream/v1/sse/flag/change`).

When unset, `eventStreams` is omitted and OFREP providers continue polling. Do not set this in AWS Lambda mode, as Lambda cannot stream SSE.

Must be an absolute `http` or `https` URL without query parameters.

- option name: `baseUrl`
- type: **string**
- default: **none**
- mandatory: No

#### `ofrepEventStream.inactivityDelaySec`

The delay in seconds advertised to the client, after which it should consider the stream inactive. When `0` the field is omitted.

- option name: `inactivityDelaySec`
- type: **integer**
- default: **`0`**
- mandatory: No

## Use multiple flag sets

Flag sets allow you to organize your feature flags into separate groups, each with its own configuration, API keys, retrievers, exporters, and notifiers. This is particularly useful for:

- **Team isolation**: Different teams can manage their own flags independently
- **Environment separation**: Separate flags for different environments (dev, staging, prod)
- **Access control**: Different API keys for different applications or teams
- **Independent configurations**: Each flag set can have its own retrievers, exporters, and notifiers

### How flag sets work

When you configure flag sets, the relay proxy operates in **flag set mode** instead of the default single configuration mode. In flag set mode:

- Each flag set requires at least one API key
- API keys are used to route requests to the appropriate flag set
- Each flag set can have its own `retrievers`, `exporters`, and `notifiers`
- There is no inheritance between flag sets - each is completely independent

### Basic flag set configuration

```yaml
server:
  mode: http
  port: 1031
flagSets:
  - name: teamA
    apiKeys:
      - "team-a-api-key-1"
      - "team-a-api-key-2"
    retrievers:
      - kind: file
        path: /path/to/team-a-flags.yaml
    exporters:
      - kind: log

  - name: teamB
    apiKeys:
      - "team-b-api-key"
    retrievers:
      - kind: s3
        bucket: my-feature-flag-bucket
        item: team-b/flags.yaml
    exporters:
      - kind: s3
        bucket: evaluation-data-bucket
    notifiers:
      - kind: slack
        webhookUrl: "https://hooks.slack.com/services/..."
```

### Flag set configuration options

> [!WARNING]
> Flag sets do not inherit flag configuration from the top level.
> Configure `retrievers`, `exporters`, `notifiers`, `pollingInterval`, and other [flag set options](#type-flagset) separately for each flag set.

Process-wide settings such as `server`, `logLevel`, `logFormat`, `authorizedKeys.admin`, and `otel` remain top-level settings and still apply in flag set mode.

### Using flag sets with API keys

When using flag sets, all evaluation requests must include an API key.  
You can do so by adding the API key in the initialization of your providers.

The relay proxy will:

1. Extract the API key from the `Authorization` header
1. Find the flag set associated with that API key
1. Route the evaluation request to that flag set
1. Return the evaluation result from that flag set's flags

### Runtime API key updates

Semaphore supports updating API keys at runtime without restarting the relay proxy.  
See the [Runtime configuration updates](#runtime-configuration-updates) section for detailed information about runtime updates in flagset mode.

> [!WARNING]
> To enable runtime API key updates, your flag sets **must have a `name` configured**.  
> Without a name, a UUID is generated automatically and runtime updates won't work.
>
> ```yaml
> flagSets:
>   - name: team-a  # ✅ Required for runtime API key updates
>     apiKeys:
>       - "team-a-api-key"
>     # ...
> ```
>

### Example: Multi-team setup

```yaml
server:
  mode: http
  port: 1031

logLevel: info

flagSets:
  # Frontend team flags
  - name: frontend
    apiKeys:
      - "frontend-api-key"
    retrievers:
      - kind: file
        path: /flags/frontend-flags.yaml
    exporters:
      - kind: s3
        bucket: frontend-evaluations
    notifiers:
      - kind: slack
        webhookUrl: "https://hooks.slack.com/services/frontend-webhook"

  # Backend team flags
  - name: backend
    apiKeys:
      - "backend-api-key"
    retrievers:
      - kind: github
        repositorySlug: my-org/backend-flags
        path: flags.yaml
    exporters:
      - kind: kafka
        kafka:
          topic: backend-evaluations
          addresses:
            - "localhost:9092"
    notifiers:
      - kind: discord
        webhookUrl: "https://discord.com/api/webhooks/backend-webhook"

  # Mobile team flags
  - name: mobile
    apiKeys:
      - "mobile-api-key"
    retrievers:
      - kind: s3
        bucket: mobile-feature-flags
        item: flags.yaml
    exporters:
      - kind: file
        outputDir: /logs/mobile-evaluations
```

## Runtime configuration updates

Semaphore relay proxy supports updating certain configuration options at runtime without requiring a restart.  
When the configuration file changes, the relay proxy automatically detects and applies the changes.

> [!WARNING]
> Changes to the `studio` configuration require restarting the relay proxy. This includes enabling or disabling Studio and changing its `url`, `headers`, `pollingInterval`, or `cacheFile` settings.

### Default mode runtime updates

In **default mode** (when not using flag sets), you can update the following configuration at runtime:

- **`authorizedKeys.evaluation`**: API keys for evaluation endpoints
- **`authorizedKeys.admin`**: API keys for admin endpoints
- **`apiKeys`**: Deprecated field (still supported for backward compatibility)

> [!WARNING]
> In default mode, **only API keys can be updated at runtime**.  
> All other configuration changes (retrievers, exporters, notifiers, polling intervals, etc.) are not supported and will be ignored with a warning log message.

**Example:**
```yaml
# Initial configuration
authorizedKeys:
  evaluation:
    - "key-1"
    - "key-2"
  admin:
    - "admin-key-1"

# Updated configuration (runtime update)
authorizedKeys:
  evaluation:
    - "key-1"
    - "key-2"
    - "key-3"  # ✅ New key added
  admin:
    - "admin-key-1"
    - "admin-key-2"  # ✅ New admin key added
```

### Flagset mode runtime updates

In **flagset mode** (when using flag sets), you can update API keys for each flag set at runtime.

**Supported operations:**
- Adding new API keys to an existing flag set
- Removing API keys from a flag set
- Replacing API keys with new ones

> [!WARNING]
> To enable runtime API key updates, your flag sets **must have a `name` configured**.  
> Without a name, a UUID is generated automatically and runtime updates won't work.
>
> ```yaml
> flagSets:
>   - name: team-a  # ✅ Required for runtime API key updates
>     apiKeys:
>       - "team-a-api-key"
>     # ...
> ```

### How runtime updates work

1. The relay proxy detects the configuration file change
2. It validates the new configuration _(if the configuration is invalid, the relay proxy will not apply the changes)_
3. It updates the internal API key mappings
4. New requests immediately use the updated routing

> [!NOTE]
> During the update, there is no downtime. Requests continue to be processed normally, and the new API key configuration takes effect immediately after the update is applied.
