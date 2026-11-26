# Events and notifiers

Evaluation events go to **exporters**, flag changes go to **notifiers**. Both are resources of the whole relay, not of a flag set. This page describes the package `cmd/relayproxy/events`.

> **Status:** the package is built beside the running relay. It will become active when the relay switches to config v2 (a later release); until then the relay keeps building one exporter pipeline per flag set, as described in the [exporter](exporter.md) and [notifier](notifier.md) pages. Everything below that says what "the relay" does describes that later release.

## The model

- `exporters` and `notifiers` are sections of the [bootstrap file](../relay-proxy/bootstrap-config.md), keyed by name.
- M exporters shared by N flag sets are **M managers**, not M x N. The number of goroutines and the memory do not depend on N: the goroutines are the workers (default 2), one per exporter, one per notifier, and the consumer of changes.
- Every exporter applies to every flag set. A `flagSets` selector is reserved for later.
- AWS backed kinds take their client from a [connection](connections.md) by name. They never build their own client and never read the AWS environment.
- The event passed to `Collect` belongs to the dispatcher from then on: the caller must not change it, the exporters read it concurrently.

## The event path and its limits

1. `Dispatcher.Collect(event)` does one non-blocking send into the **shared channel**. It takes no lock and does not allocate; with no exporter it returns at once.
2. The workers read a batch (up to 256 events) from the channel and **offer** it, without ever waiting, to the **hand-off** of each exporter.
3. Each exporter has its own goroutine that moves its hand-off to its engine manager in batches. The manager keeps the events and flushes on its schedule (`flushInterval`, `maxEventInMemory`).
4. The exporter writes its events (`Export`). The package counts what was exported, the failures, the duration of the flushes and the class of the last error. A panic of the exporter is recovered, counted as the class `panic`, logged once per instance (the type of the value only), and treated as a failed flush.

| Queue | Default | Full means |
| --- | --- | --- |
| shared channel | 65536 events (`QueueSize`) | the new event is dropped, reason `queue_full` |
| hand-off of an exporter | 16384 events (`HandoffSize`) | that exporter drops its event, reason `overflow`; the others are not touched |
| event store of the engine manager | the larger of 4 x `maxEventInMemory` and 50000 events | the manager drops, reason `overflow` (the engine manager may wait up to 200 ms for room: only the goroutine of its exporter waits) |

A slow exporter fills its own hand-off and store and loses its own events. It does not slow the workers, the shared channel or the other exporters.

## Accounting

Every accepted event is exported or counted. Per exporter name, at any time:

```text
handed = exported + overflow + drain + queued
```

- `handed`: events offered to the exporter. `queued`: handed events that are neither exported nor dropped yet.
- `overflow`: dropped by the hand-off or by the store of the manager. `drain`: handed events that could not be delivered when the exporter closed.
- For the shared channel: `accepted` = events handed to the exporters (once each) + `no_exporter` + `drain` + events still in the channel, for a set of exporters that did not change.

| Drop reason | Where | Meaning |
| --- | --- | --- |
| `queue_full` | channel | the shared channel was full |
| `shutdown` | channel | the dispatcher was shut down (or never started) |
| `no_exporter` | channel | a worker took the event while the set was empty (a reload to no exporter) |
| `drain` | channel | the shutdown deadline had passed, the worker dropped the event |
| `overflow` | exporter | the hand-off or the store of the exporter was full |
| `drain` | exporter | the exporter closed with the event undelivered |

The counters are **cumulative per exporter name**: a reload that replaces the instance of a name does not reset them. A name that changes kind keeps its counters under the new `kind` label.

## Reload

`Reload(specs)` replaces the exporters and notifiers in place, off the hot path.

- It builds every entry first. If one entry is invalid, or a side is already shut down, nothing changes, and the error names each failing entry by section, index and name (never a secret value).
- An entry whose **identity** is unchanged keeps its manager and every event it holds. The identity covers the settings, the resolved secrets, the files of the Kafka TLS settings and the build of the connection it uses.
- A new entry gets a new manager.
- A changed or removed entry leaves the set: no new event goes to it, its hand-off is delivered, its manager is stopped (which flushes it) and closed. The reload waits for these drains in parallel, for the **drain timeout** (default 10 s), which does not depend on the context of the reload: a cancelled request does not cut the drains.
- A drain that passes the deadline is **cut**: the reload reports its name and returns, and the exporter keeps closing in the background. Its pending events stay `queued` while it runs. When it finishes late they are exported (and counted as such) or, if its flush fails, counted as `drain`: never both.
- The reload does not hold the lifecycle lock while it waits, so a shutdown starts at once, and honours its own deadline, during a reload that waits for a hung exporter.
- A batch goes entirely to the old set or entirely to the new one. An event collected before the reload is delivered once to the old exporter or once to the new one, never twice. When a reload leaves **no** exporter, the events a worker takes meanwhile are counted as `no_exporter`.
- A notifier that is replaced keeps the order of the changes of a flag set: the new instance accepts changes at once but calls its notifier only after the old instance delivered what its queue held (or its drain was cut).

## Shutdown and the Start context

- `Shutdown(ctx)` stops accepting events, delivers what the channel holds, closes every exporter (one flush each) and returns a report of what was drained and dropped. The deadline of `ctx` bounds the whole: if it has passed already the workers drop the events still in the channel (counted `drain`), and an exporter still closing at the deadline is cut and keeps closing in the background (`WaitDrains` waits for it).
- A second `Shutdown` does nothing and returns the first report. Late events are dropped and counted (`shutdown`), never panic.
- The context given to `Start` ends **both** sides: when it ends the dispatcher and the notifications shut down, each with the drain timeout as its deadline. Without a changes channel (`nil`) the notifications still end.
- Apart from an exporter that was cut, no goroutine of the package is left after the shutdown.

## Notifiers

- One goroutine consumes the changes channel of the reconcile engine and offers each change to a **bounded queue per notifier** (default 64). Each notifier has one goroutine that calls it, one change at a time, in queue order: the changes of a flag set reach a notifier in order.
- A slow or failing notifier fills its own queue and loses its own changes (counted `dropped`). It never delays the consumer or the other notifiers.
- There is no per-call timeout. A call that hangs holds only the goroutine of its notifier, and it is bounded by the timeout of the HTTP client of that notifier: the Teams notifier gives each call 5 seconds, the others use the client of the engine. A cut notifier drops (and counts) what is queued behind the call.
- A change without a diff is skipped. An error is counted and logged by class only (a notifier error can carry its URL). A panic is recovered, logged with its type, and counted.
- A `Broadcast` function field is called with every change, before the notifiers, for the SSE and websocket broadcast the relay will do. It must not block.
- Counters are cumulative per notifier name, like those of the exporters.

## Supported kinds

| Section | Kinds |
| --- | --- |
| exporters | `file`, `webhook`, `log`, `s3`, `kinesis`, `sqs`, `redshift`, `googleStorage`, `kafka`, `pubsub`, `bigquery`, `azureBlobStorage`, `opentelemetry` |
| notifiers | `slack`, `microsoftteams`, `discord`, `webhook` |

- Defaults (`format`, `filename`, `csvTemplate`, `parquetCompressionCodec`, `logFormat`, `flushInterval`, `maxEventInMemory`) are those of version 1.
- `format` is accepted for `kinesis`, `kafka` and `sqs` as in version 1, but those kinds always write JSON: `csv` and `parquet` pass validation and have no effect there. `autoMigrate` is accepted for `bigquery`.
- `kafka` takes the producer settings of version 1 as keys; see [Kafka producer settings](../relay-proxy/bootstrap-config.md#kafka-producer-settings) for the table of v1 fields and v2 keys.
- `eventType: tracking` is not carried yet: such an exporter is rejected with a typed error.
- `googleCredentials` of `googleStorage` and `pubsub` is applied as a client option (version 1 ignored it for these kinds).
- The version 1 key `slackWebhookURL` is not carried over (version 2 has no aliases): use `webhookUrl`.

### AWS kinds and connections

| Kind | Connection type | Taken from the connection |
| --- | --- | --- |
| `s3` | `s3` | the S3 client (`Factory.S3`), the bucket |
| `kinesis` | `kinesis` | the AWS configuration (`Factory.AWSConfig`), the stream (unless `streamName` or `streamArn` is set) |
| `sqs` | `sqs` | the AWS configuration, the queue URL (unless `queueUrl` is set) |
| `redshift` | `redshift` | the AWS configuration |

- Exporters sharing a connection share one client or configuration: the factory builds it once.
- When an exporter leaves the set, after its final flush the runtime closes it if it holds resources (the Kafka producer, the Pub/Sub publisher and client, the BigQuery client).
- An unknown connection, a connection of the wrong type and invalid settings are typed errors that name the entry (`exporters[2] "archive"`).

## Secrets

- Secrets are read from the resolved bootstrap secrets (`fromEnv`, `fromFile`).
- They never appear in logs, errors or the status. The definitions (`ExporterDef`, `NotifierDef`) and the exporters and notifiers they hold print a redaction marker with every verb and as a slog value.
- The error of an exporter is scrubbed of the secret values of its entry before it is logged, in the raw form and as escaped for JSON, a query or a path, and as base64 (both alphabets, with or without padding). The `user:password` pair of the Kafka SASL settings is scrubbed as well. A secret shorter than 8 characters is not looked for in messages (it would hide ordinary text); this package never logs it.

## Metrics

Names and labels are closed. `exporter` and `notifier` are the names of the entries of the bootstrap file, `kind` their kind. There is no label per flag, per flag set or per event value. The collector (`events.NewCollector`) will be registered by the relay when it switches to config v2; it is not part of the version 1 metrics. Counters never reset on a reload.

| Metric | Type | Labels |
| --- | --- | --- |
| `semaphore_events_queue_capacity` | gauge | |
| `semaphore_events_queue_length` | gauge | |
| `semaphore_events_accepted_total` | counter | |
| `semaphore_events_dropped_total` | counter | `reason` (`queue_full`, `shutdown`, `no_exporter`, `drain`) |
| `semaphore_exporter_queued_events` | gauge | `exporter`, `kind` |
| `semaphore_exporter_enqueued_events_total` | counter | `exporter`, `kind` |
| `semaphore_exporter_exported_events_total` | counter | `exporter`, `kind` |
| `semaphore_exporter_dropped_events_total` | counter | `exporter`, `kind`, `reason` (`overflow`, `drain`) |
| `semaphore_exporter_flush_failures_total` | counter | `exporter`, `kind` |
| `semaphore_exporter_flush_duration_seconds` | summary (count and sum) | `exporter`, `kind` |
| `semaphore_exporter_state` | gauge | `exporter`, `kind`, `state` (`running`, `draining`, `stopped`, `cut`) |
| `semaphore_notifier_queued_changes` | gauge | `notifier`, `kind` |
| `semaphore_notifier_changes_total` | counter | `notifier`, `kind`, `result` (`delivered`, `error`, `panic`, `dropped`) |

The status snapshot (`Status()`) is immutable and read without blocking on anything slower than a counter. It has one entry per exporter name, with the state of its current instance and the cumulative counters: handed, exported, queued, drops by reason, failures, flushes and the class of the last error (`none`, `timeout`, `canceled`, `panic`, `other`).

## Not implemented yet

- Per-exporter overflow policies (`dropOldest`, `block` with a deadline): today an exporter drops its newest events.
- Per-exporter batching by `maxBytes` and retry with backoff in the pipeline: the engine manager's own logic is reused.
- The tracking overhead criterion (tracking on within 5 % of tracking off) and its soak test.
- Wiring into the relay (`main.go`) and the evaluator, and the conversion of the bootstrap connections into factory specs.
- The `flagSets` selector of an exporter.
- Exporters for tracking events (`eventType: tracking`).
