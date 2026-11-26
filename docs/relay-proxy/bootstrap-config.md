# Bootstrap file, version 2

The version 2 bootstrap file is the new configuration format of the relay proxy: connections to backends, the
control plane location, listeners, observability and admin keys. It is strict: an unknown key is an error.

> **Status.** The format, the loader and the JSON Schema are implemented in `cmd/relayproxy/bootstrap`, but the relay
> does not read this format yet. It switches to it in a later release step. From that release, version 1 files
> (the format described in [Configuration reference](configuration.md)) are rejected with `version 2 required`.

## Rules

- The file is YAML. The first key is `version: 2`. A file without it, or with another value, fails with
  `version 2 required`.
- Unknown keys are rejected. `studio` and the AWS Lambda `server.mode` are not part of version 2, so they are unknown
  keys. Inline `flagSets` is reserved and rejected: flag sets arrive from the manifest.
- Every error names the path of the key and its line, for example
  `controlPlane.manifest.pollInterval (line 12): invalid duration: expected a positive duration such as "60s"`.
  Errors never print a value.
- Durations are strings: `60s`, `1m30s`. A bare number is rejected.
- Names (connections, exporters, notifiers, listeners) match `^[a-z][a-z0-9-]{0,62}$`, are unique per kind, and
  `default` is reserved.
- A file with only `connections`, or with a `controlPlane` and nothing else optional, is valid. Without `controlPlane`
  the relay runs with no flag sets.
- The JSON Schema is generated from the Go types: `.schema/bootstrap-schema.json`.

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
appear only once.

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

- Every other `SEMAPHORE_*` variable is ignored.
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
| `auth` | object | none | see below |
| `connections` | map of name to connection | none | see below |
| `exporters` | map of name to exporter | none | see below |
| `notifiers` | map of name to notifier | none | see below |
| `controlPlane` | object | none (no control plane) | see below |

### `server`

| Key | Type | Default | Allowed |
|---|---|---|---|
| `server.unixSocketPath` | string | none | a path |
| `server.listeners[].name` | string | required | name rules |
| `server.listeners[].address` | string | required | `host:port`, for example `:1031` |
| `server.listeners[].role` | string | `api` | `api`, `ops` |
| `server.listeners[].protocols` | list of string | `[http1]` | `http1`, `h2c` (`h2` and `tls` are reserved) |

- With no listener, the relay has one `api` listener on `:1031`.
- At least one listener has the role `api`. Names and addresses are unique.

### `observability`

| Key | Type | Default | Allowed |
|---|---|---|---|
| `logging.level` | string | `info` | `debug`, `info`, `warn`, `error` |
| `logging.format` | string | `json` | `json`, `text` |
| `metrics.enabled` | boolean | `true` | |
| `metrics.legacyNames` | boolean | `false` | |
| `metrics.perFlagLabels` | boolean | `false` | |
| `tracing.otlpEndpoint` | string | none | `host:port` |
| `tracing.serviceName` | string | `semaphore` | |
| `tracing.sampler` | string | `parentbased_always_off` | `always_on`, `always_off`, `traceidratio`, `parentbased_always_on`, `parentbased_always_off`, `parentbased_traceidratio` |

### `auth`

| Key | Type | Default | Allowed |
|---|---|---|---|
| `adminKeys` | list | empty | plain strings or `{fromEnv: NAME}` / `{fromFile: PATH}` |

### `controlPlane`

| Key | Type | Default | Allowed |
|---|---|---|---|
| `connection` | string | required | name of an `s3` connection that sets a `bucket` |
| `manifest.key` | string | required (or `SEMAPHORE_MANIFEST_KEY`) | object key in the connection's bucket |
| `manifest.channel` | string | `stable` | the expected channel name |
| `manifest.pollInterval` | duration | `60s` | positive; overrides the connection's `poll.interval` |
| `manifest.jitter` | number | `0.1` | 0 to 1 |
| `manifest.maxObjects` | integer | `20000` | 1 or more |
| `stateDir` | string | none | a directory path |

### `connections.<name>`

Every connection has a `type`. The types `s3`, `kinesis`, `sqs` and `redshift` are supported. Types that are planned
but not built yet (`valkey`, `redis`, `webhook`, `kafka`, `postgres`, `mysql`, `dynamodb`, `gcs`, `azureBlob`,
`pubsub`, `bigquery`, `http`, `file`) fail with `type not supported yet`; any other value is an unknown type.

Settings shared by the four AWS types:

| Key | Type | Default | Allowed |
|---|---|---|---|
| `endpoint` | string | none (the SDK endpoint) | an `http` or `https` URL, for emulators |
| `region` | string | none (the SDK chain) | |
| `credentials.type` | string | `chain` | `chain`, `static`, `assumeRole`, `webIdentity` |
| `credentials.accessKeyId` | secret | | `static` only, required |
| `credentials.secretAccessKey` | secret | | `static` only, required |
| `credentials.sessionToken` | secret | | `static` only |
| `credentials.roleArn` | string | | `assumeRole` and `webIdentity`, required |
| `credentials.sessionName` | string | | `assumeRole` and `webIdentity` |
| `credentials.externalId` | string | | `assumeRole` only |
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
| `kinesis` | `stream` | string | none | |
| `sqs` | `queueUrl` | string | none | |

### `exporters.<name>`

`kind` is required. An exporter that needs an AWS service refers to a connection of the matching type with
`connection` (the endpoint, region and credentials of version 1 are gone). Only the schema is validated today: the
relay does not run these exporters from this file yet.

Keys valid for every kind: `kind`, `meta` (map of string), `flushInterval` (duration, default `60s`),
`maxEventInMemory` (integer, default `100000`), `eventType`.

| Kind | Connection type | Required | Other keys |
|---|---|---|---|
| `file` | | `outputDir` | `filename`, `csvTemplate`, `format`, `parquetCompressionCodec` |
| `webhook` | | `endpointUrl` (secret) | `secret` (secret), `headers` (map of secret) |
| `log` | | | `logFormat` |
| `s3` | `s3` with a `bucket` | `connection` | `path`, `filename`, `csvTemplate`, `format`, `parquetCompressionCodec` |
| `kinesis` | `kinesis` | `connection`, and a stream (`streamName`, `streamArn`, or `stream` on the connection) | |
| `googleStorage` | | `bucket` | `googleCredentials` (secret), `path`, `filename`, `csvTemplate`, `format`, `parquetCompressionCodec` |
| `sqs` | `sqs` | `connection`, and a queue (`queueUrl` here or on the connection) | |
| `kafka` | | `kafka.topic`, `kafka.addresses` | |
| `pubsub` | | `projectId`, `topic` | `googleCredentials` (secret) |
| `bigquery` | | `projectId`, `datasetId` | `tableName`, `googleCredentials` (secret) |
| `redshift` | `redshift` | `connection`, `database`, one of `clusterIdentifier` or `workgroupName` | `dbUser` (not with `workgroupName` or `secretArn`), `secretArn`, `tableName`, `autoMigrate` |
| `azureBlobStorage` | | `container`, `accountName` | `accountKey` (secret), `path`, `filename`, `csvTemplate`, `format`, `parquetCompressionCodec` |
| `opentelemetry` | | | `tracerName` |

`format` is `json` (default for the file, s3, googleStorage and azureBlobStorage kinds), `csv` or `parquet`.

### `notifiers.<name>`

`kind` is required; `meta` (map of string) is valid for every kind.

| Kind | Required | Other keys |
|---|---|---|
| `slack` | `webhookUrl` (secret) | |
| `microsoftteams` | `webhookUrl` (secret) | |
| `discord` | `webhookUrl` (secret) | |
| `webhook` | `endpointUrl` (secret) | `secret` (secret), `headers` (map of secret) |

## Full example

This file is the golden test input of the package (`cmd/relayproxy/bootstrap/testdata/bootstrap.yaml`).

```yaml
# Complete example of the version 2 bootstrap file: every section, with the secrets as references.
version: 2
server:
  listeners:
    - {name: api, address: ":1031", protocols: [http1, h2c]}
    - {name: ops, address: "127.0.0.1:1032", role: ops}
  unixSocketPath: /run/semaphore/api.sock
observability:
  logging: {level: info, format: json}
  metrics: {enabled: true, legacyNames: false, perFlagLabels: false}
  tracing:
    otlpEndpoint: "otel-collector:4317"
    serviceName: semaphore
    sampler: parentbased_always_off
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
    requestChecksumCalculation: whenRequired
    responseChecksumValidation: whenRequired
    credentials:
      type: static
      accessKeyId: {fromEnv: DEV_S3_ACCESS_KEY}
      secretAccessKey: {fromFile: /run/secrets/dev-s3-secret}
  events-kinesis: {type: kinesis, region: us-east-1, stream: acme-flag-evals}
  events-queue: {type: sqs, region: us-east-1, queueUrl: "https://sqs.us-east-1.amazonaws.com/123456789012/evals"}
  warehouse: {type: redshift, region: us-east-1}
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
```
