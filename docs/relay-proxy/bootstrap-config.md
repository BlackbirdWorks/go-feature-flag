# Bootstrap file, version 2

The version 2 bootstrap file is the new configuration format of the relay proxy: connections to backends, the
control plane location, listeners, observability and admin keys. It is strict: an unknown key is an error.

> **Status.** This is the only format the relay reads (`cmd/relayproxy/bootstrap`). A version 1 file is rejected with
> `version 2 required` and nothing starts. See [Run, reload and stop](#run-reload-and-stop) for what the relay does with it.

## Run, reload and stop

- Start: `semaphore --config bootstrap.yaml` (or `SEMAPHORE_CONFIG`). The flags `--log-level`, `--log-format`, `--listen`,
  `--ops-listen`, `--manifest-key` and `--state-dir` (given empty it means no state directory, whatever the variable and the file say) override the variables, which override the file, at the start and on
  every reload. Boolean flags (`--help`, `--version`) take `true`, `1`, `yes`, `false`, `0` or `no` in any case, only with `=` and the long name (`--help=no`; `-h=false` is a usage error). `--help` prints the flags and their variables and exits 0 (it wins over `--version`, and `--help=false` or `--version=false` request nothing); an unknown flag prints the usage on stderr and exits 2, with `--version` too (the command line is parsed strictly before the version is printed). Every reference of `auth.adminKeys` must resolve at the start: if one does not, the relay exits with a
  path-addressed error that holds no secret. An invalid or version 1 file exits with a non-zero
  status and starts nothing. After the start the process never exits because the store is unreachable: `/readyz` shows
  the state, see [probes](probes.md).
- Listeners: one `api` listener and at most one `ops` listener, each with its own address (the ops listener can bind another host, for
  example `127.0.0.1:1032`), with ports from 1 to 65535 (`:0` is refused by the relay and by `semaphore-cli config validate`, with the path of the
  address; a tool that only reads the file does not check the layout). More
  listeners, the same address twice and `server.unixSocketPath` are refused by the validation (`semaphore-cli config
  validate` and the relay agree; the publish command does not check them). `h2c` in `protocols` enables HTTP/2 over
  cleartext on the `api` listener.
- `observability.logging.format: text` writes logfmt lines.
- Reload: `SIGHUP` reads the file again. Only `auth.adminKeys` applies live; every other change is kept out and named in
  `pendingRestart` of the status, see [the core](relay-core.md#reload-in-place). A file that does not load is logged and
  refused, the relay keeps what it has. The log line `reload queued` says the file was handed to the engine; the outcome
  is the `runtime reload` line (see [the reload log line](probes.md#reload-log-line)) and the status. A reload whose admin key reference does not resolve is rejected as a
  whole and the running keys stay. A change of the file on disk is not watched: send `SIGHUP`.
- Stop: on `SIGTERM` or `SIGINT` the relay shuts down in the order described in [Shutdown](#shutdown).
- Removed with the switch: the AWS Lambda mode and artifact are gone (`/admin/v1/retriever/refresh` polls the control
  plane on request, see [advanced usage](advanced-usage.md)), and so are the retrievers other than the control plane
  store (they come back with the retriever kinds of the control plane, issue #57).

## Rules

- The file is YAML with one document. A second document (`---`) is an error. The key `version: 2` is required, in
  any position. A file without it, or with another value, fails with `version 2 required`.
- An address `host:port` (a listener, a Kafka broker, a store address, the OTLP endpoint) writes its port as digits
  only, from 1 to 65535, with no sign and no leading zero (`1031`, not `+1031` or `01031`): the file is compared as
  written and the server binds the number. An address that breaks the rule is refused with the path of the key.
- Unknown keys are rejected. `studio` and the AWS Lambda `server.mode` are not part of version 2, so they are unknown
  keys. Inline `flagSets` is reserved and rejected: flag sets arrive from the manifest.
- Every error names the path of the key and its line, for example
  `controlPlane.manifest.pollInterval (line 12): invalid duration: expected a positive duration such as "60s"`.
  An error never contains a secret or an admin key. It does name keys, types and references written in the file
  (for example an unknown key or a connection name), except under `auth`, where an unknown key is reported by path
  and line only, so that a mis-indented admin key is not echoed.
- Scalars keep their YAML type: a string setting must be a string (write `bucket: "123"`, not `bucket: 123`), an
  integer setting must be an integer (`3`, not `3.0`), and a boolean must be `true` or `false`.
- URLs and addresses that are printed (`endpoint`, `queueUrl`, `tracing.otlpEndpoint`, Kafka addresses) must not
  contain credentials (`http://user:pass@host`). Put them in a secret reference or a credentials setting.
- Durations are strings: `60s`, `1m30s`. A bare number is rejected.
- A key written for another kind of exporter, notifier or credentials is an error even with a zero value
  (`autoMigrate: false` on a `log` exporter).
- Names (connections, exporters, notifiers, listeners) match `^[a-z][a-z0-9-]{0,62}$`, are unique per kind, and
  `default` is reserved.
- A file with only `connections`, or with a `controlPlane` and nothing else optional, is valid. Without `controlPlane`
  the relay runs with no flag sets.
- The JSON Schema is generated from the Go types: `.schema/bootstrap-schema.json`. It carries the enumerations,
  ranges, defaults, name patterns and the keys required, and the keys not allowed, per kind and per credentials type (an absent or empty
  credentials `type` is `chain`). It also carries the Redshift rules: exactly one of `clusterIdentifier` and
  `workgroupName`, and `dbUser` not with `workgroupName` or `secretArn`. An empty string for an enumerated
  setting (for example `format: ""`) means "unset" and takes the default. The loader stays the
  authority: some rules (references between sections, uniqueness, host and URL checks) are not in the schema.

## Secrets

Connection credentials, webhook URLs and header values are `Secret` values, written as a reference:

```yaml
accessKeyId: {fromEnv: DEV_S3_ACCESS_KEY}          # value of the environment variable
secretAccessKey: {fromFile: /run/secrets/s3-key}   # content of the file, trailing newline removed
```

- An inline plain string is rejected (`inline secret not allowed`) unless the loader runs with the development flag
  (`LoadOptions.AllowInlineSecrets`).
- A secret never prints: `String`, `%v`, `%+v`, JSON, YAML and slog output all give `[REDACTED]`. The effective print
  shows the reference (`fromEnv: NAME`), never the value.

### API keys are plain strings

`auth.adminKeys` is a list of plain strings (the keys are UUIDs set dynamically) or secret references. No development
flag is needed. The keys are still never printed in errors, logs or the effective print (`[REDACTED]`), and a key may
appear only once. An admin key that equals an API key of a flag set is disabled, and the flag set does not get that key either: it
reports `admin-key-collision`, see [admin key equal to a flag-set key](probes.md#admin-key-equal-to-a-flag-set-key).

## Environment variables

Only these variables are read. Precedence: command-line flag, then variable, then file.

| Variable | Meaning |
|---|---|
| `SEMAPHORE_CONFIG` | Path of the bootstrap file (same as `--config`) |
| `SEMAPHORE_LOG_LEVEL` | `observability.logging.level` |
| `SEMAPHORE_LOG_FORMAT` | `observability.logging.format` |
| `SEMAPHORE_LISTEN` | Address of the `api` listener, for example `:8080` (created when missing) |
| `SEMAPHORE_OPS_LISTEN` | Address of the `ops` listener (created when missing) |
| `SEMAPHORE_MANIFEST_KEY` | `controlPlane.manifest.key` |
| `SEMAPHORE_STATE_DIR` | `controlPlane.stateDir` |
| `SEMAPHORE_SHUTDOWN_DRAIN_DELAY` | `server.shutdown.drainDelay` |
| `SEMAPHORE_SHUTDOWN_TIMEOUT` | `server.shutdown.timeout` |

- Every other `SEMAPHORE_*` variable is ignored.
- A value from a flag or variable that fails validation is reported with its source (`set by flag --listen`,
  `set by environment variable SEMAPHORE_LISTEN`) and no file line. The two shutdown variables have no flag. The flags are `--config`, `--log-level`,
  `--log-format`, `--listen`, `--ops-listen`, `--manifest-key` and `--state-dir`.
- `SEMAPHORE_OPS_LISTEN` sets the address of the first `ops` listener. When there is none it adds a listener named
  `ops` (or `ops-2`, ... when that name is taken).
- The JSON Schema describes the file before overrides, so `controlPlane.manifest.key` and `listeners[].address` are
  optional in it; the loader still requires an effective value.
- `SEMAPHORE_MANIFEST_KEY` and `SEMAPHORE_STATE_DIR` only apply when the file has a `controlPlane`.
- The standard AWS SDK variables keep working for `credentials.type: chain`, and the OpenTelemetry SDK variables for
  tracing. Semaphore does not map them.

## Keys and defaults

### Top level

| Key | Type | Default | Allowed |
|---|---|---|---|
| `version` | integer | required | `2` |
| `server` | object | one api listener | see below |
| `observability` | object | see below | see below |
| `evaluation` | object | none | see below |
| `auth` | object | none | see below |
| `connections` | map of name to connection | none | see below |
| `exporters` | map of name to exporter | none | see below |
| `notifiers` | map of name to notifier | none | see below |
| `controlPlane` | object | none (no control plane) | see below |
| `segments` | object | none (no segment) | see below |

### `server`

| Key | Type | Default | Allowed |
|---|---|---|---|
| `server.unixSocketPath` | string | none | a path |
| `server.listeners[].name` | string | required | name rules |
| `server.listeners[].address` | string | required | `host:port`, for example `:1031` |
| `server.listeners[].role` | string | `api` | `api`, `ops` |
| `server.listeners[].protocols` | list of string | `[http1]` | `http1`, `h2c`, no duplicates (`h2` and `tls` are reserved) |
| `server.shutdown.drainDelay` | duration | `0s` (no delay) | `0s` to `10m` |
| `server.shutdown.timeout` | duration | `15s` | positive, at most `10m` |
| `server.hideBanner` | bool | `false` | keeps the banner out of the standard output at start |
| `server.ops.pprof.enabled` | bool | `false` | needs a listener with the role `ops` |
| `server.ofrepEventStream.baseUrl` | string | none (off) | absolute `http` or `https` URL, no credentials, query or fragment |
| `server.ofrepEventStream.inactivityDelaySec` | integer | `0` (not advertised) | zero or more |
| `server.validateFlags.maxJsonBodySize` | size | `1MiB` | a size string such as `2MiB`, `64KiB` or `1500B`; `0B` means the default |
| `server.validateFlags.maxYamlBodySize` | size | `64KiB` | as above |
| `server.validateFlags.maxTomlBodySize` | size | `64KiB` | as above |

- With no listener, the relay has one `api` listener on `:1031`.
- At least one listener has the role `api`. Names and addresses are unique.
- The shutdown settings are described in [Shutdown](#shutdown).
- `server.ops.pprof.enabled` serves the Go profiler under `/debug/pprof/` on the `ops` listener and never on the `api`
  listener; see [Profiling](profiling.md). With no `ops` listener the validation refuses it.
- `server.ofrepEventStream.baseUrl` is the public base URL clients use to reach the relay. When it is set, the OFREP bulk
  evaluation advertises the flag change stream (`/stream/v1/sse/flag/change`) in `eventStreams`, with the API key of the
  caller in the URL, so that a provider subscribes instead of polling. `inactivityDelaySec` is the delay after which a
  client takes the stream as inactive; `0` leaves it out.
- `server.validateFlags.*` limit the body of `POST /admin/v1/flags/validate` by format. A larger body gets a `413`.
- A change of `server.ops`, `server.ofrepEventStream`, `server.validateFlags` or `server.hideBanner` on `SIGHUP` is listed
  in `pendingRestart` (`server.ops`, ...) and applies after a restart. The version header is always sent, and there is no
  Swagger UI: the OpenAPI document of the relay is enough.

## Shutdown

`SIGTERM` or `SIGINT` starts the shutdown. A second `SIGTERM` or `SIGINT` while it runs stops the relay hard at once.
The steps run in this order:

1. `/readyz` answers 503 `shutting-down`; the listeners stay open.
2. The drain delay (`server.shutdown.drainDelay`, or `SEMAPHORE_SHUTDOWN_DRAIN_DELAY`): the relay keeps serving every
   request, so that load balancers and Kubernetes stop sending traffic. `0s` (the default) means no delay.
3. The change streams close, so that their clients reconnect elsewhere and the server can drain.
4. The HTTP servers stop gracefully: the requests in flight finish.
5. The core stops: the event queues are flushed to the exporters and the state directory is written.

The timeout (`server.shutdown.timeout`, or `SEMAPHORE_SHUTDOWN_TIMEOUT`, default `15s`) starts after the drain delay and
bounds steps 3 to 5. The drain delay is not part of it, so the longest a shutdown takes is the drain delay plus the
timeout. When the timeout ends first the relay stops hard: the servers close, the requests still in flight are cut, the
log lists what was cut (`shutdown: stopped hard`), and the process exits with status 1. A clean shutdown exits with 0.
Both settings are read at the start (a reload does not change them), are durations of at most `10m` (the timeout is positive, the drain delay may be `0s`), and the variable wins over the file, and a value that does not parse
stops the relay at the start. See [probes](probes.md#shutdown-in-kubernetes) for Kubernetes.

### `observability`

| Key | Type | Default | Allowed |
|---|---|---|---|
| `logging.level` | string | `info` | `debug`, `info`, `warn`, `error` |
| `logging.format` | string | `json` | `json`, `text` |
| `metrics.enabled` | boolean | `true` | |
| `metrics.legacyNames` | boolean | `false` | |
| `metrics.perFlagLabels` | boolean | `false` | |
| `tracing.otlpEndpoint` | string | none | `host:port`, or an `http`/`https` URL, without credentials |
| `tracing.serviceName` | string | `semaphore` | |
| `tracing.sampler` | string | `parentbased_always_off` | `always_on`, `always_off`, `traceidratio`, `parentbased_always_on`, `parentbased_always_off`, `parentbased_traceidratio` |

The sampler is given to the OpenTelemetry SDK through `OTEL_TRACES_SAMPLER` when that variable is not set (the variable of the operator wins); the ratio of the `traceidratio` samplers comes from `OTEL_TRACES_SAMPLER_ARG`.

### `evaluation`

| Key | Type | Default | Allowed |
|---|---|---|---|
| `contextEnrichment` | map | none | strings, integers, finite numbers, booleans, lists and mappings with string keys (no timestamps, binary or infinity: quote the text); merged into the context of every evaluation, and it wins over a request attribute of the same name, as in version 1 |
| `environment` | string | none | added to the enrichment under the key `env` |
| `disableSemaphoreMetadata` | boolean | `false` | keeps `semaphore_cacheable` out of the OFREP metadata; `gofeatureflag_cacheable` is always sent |

A change of `evaluation` needs a restart (it is listed in `pendingRestart` after a reload).

### `auth`

| Key | Type | Default | Allowed |
|---|---|---|---|
| `adminKeys` | list | empty | plain strings or `{fromEnv: NAME}` / `{fromFile: PATH}` |

Public flags are not a bootstrap setting: see [Public flags](#public-flags).

### Public flags

A flag set that is published **without API keys** (no `apiKeys` in its entry of the flag-set document) is public: a request without a key is served by the public flags: the evaluation, the bulk evaluation, OFREP, the flag change and the streams, everything a key of a flag set can do, and never the admin endpoints. Like the other flag sets, it changes with the published generation: no `SIGHUP`, no restart.

- Several flag sets without keys form **one public namespace**. A flag key that two of them define is an error: the generation is rejected as a whole, the relay keeps the last known good (`code: build-failed`, with the flag key and the two flag sets in the log). `semaphore-cli publish` and `publish verify` [catch it before the upload](../tooling/publish.md#flag-sets-without-api-keys).
- A request **with** a key behaves as before: a wrong key is `401`, it never falls back to the public flags.
- When the generation has no flag set without keys, a request without a key is refused (`401`). When the generation that publishes the keys of the last public flag set arrives, the open streams of the clients without a key are closed (they reconnect and are refused).
- **What a caller without a key can do:** it is a caller of the public flags only. It evaluates them (single, bulk,
  OFREP), reads their configuration, change hashes and manifest, follows their changes on the streams, and sends
  evaluation events for them. It sees nothing of the flags of a flag set with a key: they answer as flags that do not
  exist, and an evaluation event for any other flag key is dropped and counted (`semaphore_events_dropped_total` with
  `reason="not_public"`), with the same answer as for any other batch. The admin endpoints need an admin key.
- `/info` shows the status of the namespace: `"public": {"present": true, "flags": 12, "sets": ["pricing", "web"]}`.

### `controlPlane`

| Key | Type | Default | Allowed |
|---|---|---|---|
| `connection` | string | required | name of an `s3` connection that sets a `bucket`, or of a `file` connection |
| `manifest.key` | string | required (or `SEMAPHORE_MANIFEST_KEY`) | S3: an object key without a leading slash. File: a clean relative path below the directory (no `./`, no `..`, not absolute) |
| `manifest.channel` | string | `stable` | the expected channel name, not blank |
| `manifest.pollInterval` | duration | `60s` | at least `1s` (checked by the loader and `config validate`, not by the JSON Schema; `publish` does not check it); overrides the connection's `poll.interval` |
| `manifest.jitter` | number | `0.1` | 0 up to, not including, 1 |
| `manifest.maxObjects` | integer | `20000` | 1 or more |
| `stateDir` | string | none | a directory path |

`controlPlane.stateDir` is where the relay keeps the generation it serves, so that a restart while the control plane is
unreachable serves it at once. The directory holds **what the relay serves**, and only that:

- It is written after the engine stored the content of a generation, and it is derived from the running runtime: the
  manifest of the generation that runs and, for every object, the bytes of the checksum the runtime holds. A
  generation that the engine rejects (a build failure, a flag key defined in two public flag sets) is never written, a
  change of the bootstrap file (an admin key) that is stored in the same round as rejected content writes nothing of
  that content, and a first generation that is never applied leaves no manifest or object on disk.
- An object the runtime holds from earlier content (its download failed, or its bytes could not be decoded) is written
  with the bytes the runtime holds, as a stale object, never with the latest bytes the reader fetched for its name. At
  restart it is stale with the same bytes.
- The write happens after the swap, off the request path and off the engine: the relay never waits for the disk. Objects whose checksum is already on disk are not written again, and an identical state is not written at all. After a failed save the store does not trust its earlier writes: it may write an object again although its checksum file exists (an extra write, never a skipped one). The
  files are written to a temporary name and renamed, so a crash leaves the previous state readable, and a half
  written file is never loaded.
- **What a restart does not restore**: a flag that fails validation keeps serving its earlier definition while the relay
  runs. The directory holds the bytes of the object, not that earlier definition, so a restart serves no such flag
  (the other flags of the object are served). `/info` counts them: `objects[].kept` per object and
  `config.state.keptFlags` in all.
- A write that fails (a full disk, a permission) is logged, counted in `semaphore_manifest_state_saves_total` with `result="error"`
  (its labels are the connection and the result), and its closed reason is `config.state.lastSaveError` in `/info`; both are current when the write ends. The relay goes on serving. At shutdown the
  last applied generation is written within the shutdown deadline.

### `connections.<name>`

Every connection has a `type`. The types `s3`, `kinesis`, `sqs`, `redshift`, `file`, `redis` and `valkey` are supported. Types that are planned
but not built yet (`webhook`, `kafka`, `postgres`, `mysql`, `dynamodb`, `gcs`, `azureBlob`,
`pubsub`, `bigquery`, `http`) fail with `type not supported yet`; any other value is an unknown type.

Settings shared by the AWS types (`s3`, `kinesis`, `sqs`, `redshift`):

| Key | Type | Default | Allowed |
|---|---|---|---|
| `endpoint` | string | none (the SDK endpoint) | an `http` or `https` URL without credentials, for emulators |
| `region` | string | required for `s3` | non-blank; never read from the environment or the shared config files |
| `caBundleFile` | string | none (the system roots) | a PEM file of certificate authorities trusted in addition, for TLS emulators and private stores |
| `credentials.type` | string | `chain` | `chain`, `static`, `assumeRole`, `webIdentity` |
| `credentials.accessKeyId` | secret | | `static` only, required |
| `credentials.secretAccessKey` | secret | | `static` only, required |
| `credentials.sessionToken` | secret | | `static` only |
| `credentials.roleArn` | string | | `assumeRole` and `webIdentity`, required |
| `credentials.sessionName` | string | | `assumeRole` and `webIdentity` |
| `credentials.externalId` | secret | | `assumeRole` only |
| `credentials.tokenFile` | string | | `webIdentity` only, required |
| `timeouts.connect` | duration | `10s` | positive |
| `timeouts.request` | duration | `30s` | positive |
| `retry.maxAttempts` | integer | `3` | 1 to 20 |
| `retry.maxBackoff` | duration | `20s` | positive |

Extra keys per type:

| Type | Key | Type | Default | Allowed |
|---|---|---|---|---|
| `s3` | `bucket` | string | none | required when the connection is the control plane |
| `s3` | `pathStyle` | boolean | `false` | |
| `s3` | `poll.interval` | duration | `60s` | positive |
| `s3` | `poll.jitter` | number | `0.1` | 0 to 1 |
| `s3` | `requestChecksumCalculation` | string | `whenSupported` | `whenSupported`, `whenRequired` |
| `s3` | `responseChecksumValidation` | string | `whenSupported` | `whenSupported`, `whenRequired` |
| `file` | `path` | string | required | a directory; object keys are read below it, and no key can leave it |
| `kinesis` | `stream` | string | none | |
| `sqs` | `queueUrl` | string | none | an `http` or `https` URL without credentials |

Settings of the store types `redis` and `valkey` (stores of [dynamic segments](../configure-flags/segments-dynamic.md); see the pages of the [redis](../configure-flags/segments-redis.md) and [valkey](../configure-flags/segments-valkey.md) providers for what they do):

| Type | Key | Type | Default | Allowed |
|---|---|---|---|---|
| both | `addresses` | list of strings | required | `host:port` (IPv6 in brackets), no credentials, no white space, no duplicate; at least one |
| both | `username` | string | none | |
| both | `password` | secret | none | `fromEnv` or `fromFile` (an inline value is refused unless inline secrets are allowed) |
| both | `tls.enable` | boolean | `false` | |
| both | `tls.caBundleFile` | string | none (the system roots) | a PEM file; needs `tls.enable: true` |
| both | `tls.serverName` | string | none | needs `tls.enable: true` |
| both | `keyPrefix` | string | `semaphore:segment:` | no white space |
| both | `lookupTimeout` | duration | `100ms` | positive; bounds one lookup call |
| `redis` | `cluster` | boolean | `false` | a single address that is a cluster endpoint; several addresses are a cluster already |
| `redis` | `db` | integer | `0` | 0 or more; not with a cluster |
| `redis` | `timeouts.connect`, `timeouts.read`, `timeouts.write` | duration | go-redis defaults | positive |
| `redis` | `maxRetries` | integer | go-redis default | -1 or more (-1 turns the retries off) |
| `redis` | `poolSize` | integer | go-redis default | 1 or more |
| `redis` | `atomic` | boolean | `false` | one Lua script for each set segment |
| `valkey` | `pipelining` | string | `always` | `always`, `auto` |
| `valkey` | `blockingPipeline` | integer | `0` (never) | 0 or more |
| `valkey` | `retry` | boolean | `false` | |
| `valkey` | `forceSingleClient` | boolean | `false` | one address only; keeps valkey-go from probing for a cluster |
| `valkey` | `timeouts.connect`, `timeouts.io` | duration | valkey-go defaults | positive; `io` is the read and write timeout of a connection |

### `segments`

Process-level settings of the segment resolver (cache, breaker, timeouts). The segment definitions are not in this file: they are published with the flags and arrive through the control plane (not active yet). A definition names one of the `redis` or `valkey` connections above.

The relay validates this section and logs that segment stores are configured but not active: the relay does not look a segment up yet.

| Key | Type | Default | Allowed |
|---|---|---|---|
| `resolver.callTimeout` | duration | `2s` | positive |
| `resolver.shutdownGrace` | duration | `5s` | positive |
| `resolver.maxOverdue` | integer | `8` | 1 or more |
| `resolver.maxIdBytes` | integer | `512` | 1 or more |
| `resolver.default.normalize` | string | `none` | `none`, `trimLower` |
| `resolver.default.ttl` | duration | `30s` | positive; how long a member is cached |
| `resolver.default.negativeTtl` | duration | `5s` | positive |
| `resolver.default.maxEntries` | integer | `10000` | 1 or more |
| `resolver.default.shards` | integer | `16` | 1 or more |
| `resolver.default.breakerFailures` | integer | `5` | 1 or more |
| `resolver.default.breakerCooldown` | duration | `10s` | positive |
| `resolver.default.noCache`, `resolver.default.noBreaker` | boolean | `false` | |

### `exporters.<name>`

`kind` is required. An exporter that needs an AWS service refers to a connection of the matching type with
`connection` (the endpoint, region and credentials of version 1 are gone). Only the schema is validated today: the
relay does not run these exporters from this file yet.

Keys valid for every kind: `kind`, `meta` (map of string), `flushInterval` (duration, default `60s`),
`maxEventInMemory` (integer, 1 or more, default `100000`), `eventType`. A `queueUrl` is an `http` or `https` URL
without credentials.

| Kind | Connection type | Required | Other keys |
|---|---|---|---|
| `file` | | `outputDir` | `filename`, `csvTemplate`, `format`, `parquetCompressionCodec` |
| `webhook` | | `endpointUrl` (secret) | `secret` (secret), `headers` (map of secret) |
| `log` | | | `logFormat` |
| `s3` | `s3` with a `bucket` | `connection` | `path`, `filename`, `csvTemplate`, `format`, `parquetCompressionCodec` |
| `kinesis` | `kinesis` | `connection`, and a stream (`streamName`, `streamArn`, or `stream` on the connection) | `format` |
| `googleStorage` | | `bucket` | `googleCredentials` (secret), `path`, `filename`, `csvTemplate`, `format`, `parquetCompressionCodec` |
| `sqs` | `sqs` | `connection`, and a queue (`queueUrl` here or on the connection) | `format` (accepted and ignored, as in version 1) |
| `kafka` | | `kafka.topic`, `kafka.addresses` (each `host:port`, without credentials) | `format`, the producer settings of `kafka` (see [Kafka producer settings](#kafka-producer-settings)) |
| `pubsub` | | `projectId`, `topic` | `googleCredentials` (secret) |
| `bigquery` | | `projectId`, `datasetId` | `tableName`, `googleCredentials` (secret), `autoMigrate` |
| `redshift` | `redshift` | `connection`, `database`, one of `clusterIdentifier` or `workgroupName` | `dbUser` (not with `workgroupName` or `secretArn`), `secretArn`, `tableName`, `autoMigrate` |
| `azureBlobStorage` | | `container`, `accountName` | `accountKey` (secret), `path`, `filename`, `csvTemplate`, `format`, `parquetCompressionCodec` |
| `opentelemetry` | | | `tracerName` |

`format` is `json` (default for the file, s3, googleStorage and azureBlobStorage kinds), `csv` or `parquet`. The `kinesis`, `kafka` and `sqs` kinds accept it as version 1 did but always write JSON (`sqs` also ignores it for its message body): `csv` and `parquet` are valid there and have no effect.

### Kafka producer settings

Version 1 took any scalar field of the sarama configuration under `kafka.config`. Version 2 names the fields a producer uses as keys of `kafka`. A zero or absent value keeps the sarama default (version 1 could not tell zero from unset either). `Producer.Return.Successes` and `Producer.Return.Errors` are always true. Passwords are secrets (`fromEnv` or `fromFile`). The files of `net.tls` are read when the exporter is built; a rotated file rebuilds the exporter at the next reload.

| v1 field (sarama) | v2 key under `kafka` |
|---|---|
| `Version` | `version` (such as `3.6.0`) |
| `ClientID`, `RackID`, `ChannelBufferSize`, `ApiVersionsRequest` | `clientId`, `rackId`, `channelBufferSize`, `apiVersionsRequest` |
| `Net.MaxOpenRequests`, `DialTimeout`, `ReadTimeout`, `WriteTimeout`, `KeepAlive`, `ResolveCanonicalBootstrapServers` | `net.maxOpenRequests`, `dialTimeout`, `readTimeout`, `writeTimeout`, `keepAlive`, `resolveCanonicalBootstrapServers` (durations are strings such as `5s`) |
| `Net.TLS.Enable` | `net.tls.enable` |
| `Net.TLS.Config` `InsecureSkipVerify`, `ServerName`, `RootCAs`, `Certificates` | `net.tls.insecureSkipVerify`, `serverName`, `caFile`, `certFile` with `keyFile` (PEM files) |
| `Net.SASL` `Enable`, `Handshake`, `Mechanism`, `User`, `AuthIdentity`, `SCRAMAuthzID`, `Version` | `net.sasl.enable`, `handshake`, `mechanism` (`PLAIN`, `SCRAM-SHA-256`, `SCRAM-SHA-512`, `GSSAPI`), `user`, `authIdentity`, `scramAuthzId`, `version` |
| `Net.SASL.Password` | `net.sasl.password` (secret) |
| `Net.SASL.GSSAPI` `AuthType`, `KeyTabPath`, `KerberosConfigPath`, `ServiceName`, `Username`, `Realm`, `DisablePAFXFAST` | `net.sasl.gssapi.authType` (`user` or `keytab`), `keyTabPath`, `kerberosConfigPath`, `serviceName`, `username`, `realm`, `disablePafxfast` |
| `Net.SASL.GSSAPI.Password` | `net.sasl.gssapi.password` (secret) |
| `Metadata` `Retry.Max`, `Retry.Backoff` (no buffer limits), `RefreshFrequency`, `Full`, `Timeout`, `AllowAutoTopicCreation`, `SingleFlight` | `metadata.retry.max`, `retry.backoff`, `refreshFrequency`, `full`, `timeout`, `allowAutoTopicCreation`, `singleFlight` |
| `Producer` `MaxMessageBytes`, `Timeout`, `Idempotent`, `CompressionLevel` | `producer.maxMessageBytes`, `timeout`, `idempotent`, `compressionLevel` |
| `Producer.RequiredAcks` | `producer.requiredAcks` (`none`, `local`, `all`) |
| `Producer.Compression` | `producer.compression` (`none`, `gzip`, `snappy`, `lz4`, `zstd`) |
| `Producer.Partitioner` | `producer.partitioner` (`hash`, `random`, `roundrobin`) |
| `Producer.Retry` `Max`, `Backoff`, `MaxBufferLength`, `MaxBufferBytes` (the buffer limits only exist here) | `producer.retry.max`, `backoff`, `maxBufferLength`, `maxBufferBytes` |
| `Producer.Flush` `Bytes`, `Messages`, `Frequency`, `MaxMessages` | `producer.flush.bytes`, `messages`, `frequency`, `maxMessages` |
| `Producer.Transaction` `ID`, `Timeout`, `Retry.Max`, `Retry.Backoff` | `producer.transaction.id`, `timeout`, `retry.max`, `retry.backoff` |

Not carried over: the fields that hold code (`Net.SASL.TokenProvider`, `Net.Proxy.Dialer`, `Net.LocalAddr`, the `BackoffFunc` fields, `Producer.Interceptors`, `MetricRegistry`), and `Consumer` and `Admin`, which a producer does not use. `Net.SASL.SCRAMClientGeneratorFunc` is set from the mechanism, as in version 1.

The deprecated version 1 notifier key `slackWebhookURL` is not carried over: version 2 has no version 1 aliases, use `webhookUrl`.

### `notifiers.<name>`

`kind` is required; `meta` (map of string) is valid for every kind.

| Kind | Required | Other keys |
|---|---|---|
| `slack` | `webhookUrl` (secret) | |
| `microsoftteams` | `webhookUrl` (secret) | |
| `discord` | `webhookUrl` (secret) | |
| `webhook` | `endpointUrl` (secret) | `secret` (secret), `headers` (map of secret) |

## Full example

This file is the golden test input of the package (`cmd/relayproxy/bootstrap/testdata/bootstrap.yaml`): a test checks that this block is that file, and that it passes `semaphore-cli config validate`.

<!-- full-example -->
```yaml
# Complete example of the version 2 bootstrap file: every section, with the secrets as references.
version: 2
server:
  listeners:
    - {name: api, address: ":1031", protocols: [http1, h2c]}
    - {name: ops, address: ":1032", role: ops}
  hideBanner: false
  ops:
    pprof: {enabled: true}
  ofrepEventStream: {baseUrl: "https://flags.example.com", inactivityDelaySec: 120}
  validateFlags: {maxJsonBodySize: 2MiB, maxYamlBodySize: 128KiB, maxTomlBodySize: 128KiB}
observability:
  logging: {level: info, format: json}
  metrics: {enabled: true, legacyNames: false, perFlagLabels: false}
  tracing:
    otlpEndpoint: "otel-collector:4317"
    serviceName: semaphore
    sampler: parentbased_always_off
evaluation:
  environment: production
  contextEnrichment: {region: us-east-1}
  disableSemaphoreMetadata: false
connections:
  control:
    type: s3
    region: us-east-1
    bucket: acme-control
    poll: {interval: 60s, jitter: 0.1}
  flags-eu:
    type: s3
    region: eu-west-1
    bucket: acme-flags-eu
    credentials:
      type: assumeRole
      roleArn: arn:aws:iam::123456789012:role/semaphore-reader
      sessionName: semaphore
    timeouts: {connect: 5s, request: 20s}
    retry: {maxAttempts: 5, maxBackoff: 30s}
  dev-s3:
    type: s3
    endpoint: http://localhost:4566
    region: us-east-1
    bucket: dev-flags
    pathStyle: true
    caBundleFile: /etc/ssl/emulator-ca.pem
    requestChecksumCalculation: whenRequired
    responseChecksumValidation: whenRequired
    credentials:
      type: static
      accessKeyId: {fromEnv: DEV_S3_ACCESS_KEY}
      secretAccessKey: {fromFile: /run/secrets/dev-s3-secret}
  events-kinesis: {type: kinesis, region: us-east-1, stream: acme-flag-evals}
  events-queue: {type: sqs, region: us-east-1, queueUrl: "https://sqs.us-east-1.amazonaws.com/123456789012/evals"}
  warehouse: {type: redshift, region: us-east-1}
  local-control: {type: file, path: /var/lib/semaphore/control}
  segments-valkey:
    type: valkey
    addresses: ["valkey-1:6379", "valkey-2:6379"]
    username: semaphore
    password: {fromEnv: VALKEY_PASSWORD}
    tls: {enable: true, caBundleFile: /etc/ssl/valkey-ca.pem, serverName: valkey.internal}
    timeouts: {connect: 2s, io: 1s}
    pipelining: auto
    blockingPipeline: 64
    retry: false
    keyPrefix: "acme:segment:"
    lookupTimeout: 150ms
  segments-redis:
    type: redis
    addresses: ["redis:6379"]
    db: 2
    password: {fromFile: /run/secrets/redis-password}
    timeouts: {connect: 2s, read: 500ms, write: 500ms}
    maxRetries: 2
    poolSize: 20
    atomic: true
exporters:
  evals-kinesis:
    kind: kinesis
    connection: events-kinesis
  evals-archive:
    kind: s3
    connection: flags-eu
    path: evaluations/
    format: parquet
    parquetCompressionCodec: SNAPPY
  evals-log:
    kind: log
  evals-webhook:
    kind: webhook
    endpointUrl: {fromEnv: EVALS_WEBHOOK_URL}
    headers:
      Authorization: {fromEnv: EVALS_WEBHOOK_TOKEN}
  evals-warehouse:
    kind: redshift
    connection: warehouse
    database: analytics
    workgroupName: semaphore
    tableName: flag_evaluations
  evals-kafka:
    kind: kafka
    kafka: {topic: flag-evaluations, addresses: ["kafka-1:9092"]}
notifiers:
  team-slack:
    kind: slack
    webhookUrl: {fromEnv: SLACK_WEBHOOK_URL}
  audit-hook:
    kind: webhook
    endpointUrl: {fromFile: /run/secrets/audit-url}
    secret: {fromFile: /run/secrets/audit-signing}
auth:
  adminKeys:
    - 7d9f1e0c-6a43-4c2e-9d57-0f6b3f0f1a11
    - {fromEnv: SEMAPHORE_ADMIN_KEY}
controlPlane:
  connection: control
  manifest:
    key: manifests/prod-us-east-1/stable.json
    channel: stable
    pollInterval: 60s
    jitter: 0.1
    maxObjects: 20000
  stateDir: /var/lib/semaphore
segments:
  resolver:
    callTimeout: 2s
    shutdownGrace: 5s
    maxOverdue: 8
    maxIdBytes: 512
    default: {normalize: trimLower, ttl: 30s, negativeTtl: 5s, maxEntries: 10000, shards: 16, breakerFailures: 5, breakerCooldown: 10s}
```
