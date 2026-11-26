# Events and notifiers

Evaluation events go to **exporters**, flag changes go to **notifiers**. Both are resources of the whole relay, not of a flag set. This page describes the package `cmd/relayproxy/events`.

> **Status:** the package is built beside the running relay. It becomes active when the relay switches to config v2 (a later release). Until then the relay keeps building one exporter pipeline per flag set, as described in the [exporter](exporter.md) and [notifier](notifier.md) pages.

## The model

- `exporters` and `notifiers` are sections of the [bootstrap file](../relay-proxy/bootstrap-config.md), keyed by name.
- M exporters shared by N flag sets are **M managers**, not M x N. The number of goroutines and the memory do not depend on N.
- Every exporter applies to every flag set. A `flagSets` selector is reserved for later.
- One bounded channel of events and a small **fixed** worker pool sit between the evaluation and the exporters.
- AWS backed kinds take their client from a [connection](connections.md) by name. They never build their own client and never read the AWS environment.

## The event path

1. `Dispatcher.Collect(event)` does one non-blocking send into the channel. It does not allocate, and with no exporter it returns at once.
2. The workers (default 2) read a batch (up to 256 events) from the channel.
3. A worker hands the batch to **every** exporter manager of the current set. Each manager keeps its own events and flushes on its own schedule (`flushInterval`, `maxEventInMemory`).
4. Each exporter writes its events (`Export`). The package counts what was exported, the failures, and the class of the last error.

A slow exporter holds its own manager only until the manager gives up waiting (the engine manager waits at most 200 ms for room, then drops and counts). Per-exporter queues with a choice of overflow policy are not part of this release.

## Overflow and drop accounting

- When the channel is full, `Collect` drops the new event and counts it. It never waits and the channel never grows (**drop newest**).
- After a shutdown, `Collect` drops and counts, and never panics.
- Every drop has a reason from a closed set:

| Reason | Meaning |
| --- | --- |
| `queue_full` | the event channel was full |
| `shutdown` | the dispatcher was shut down |
| `overflow` | the queue of an exporter overflowed (its manager dropped the event) |
| `drain` | the event was not delivered when its exporter closed (deadline passed or final flush failed) |

## Reload

`Reload(specs)` replaces the exporters and notifiers in place, off the hot path.

- It builds every entry first. If one entry is invalid nothing changes, and the error names each failing entry by section, index and name (never a secret value).
- An entry whose **identity** is unchanged keeps its manager and every event it holds. The identity covers the settings, the resolved secrets and the build of the connection it uses.
- A new entry gets a new manager.
- A changed or removed entry leaves the set: no new event goes to it, it is stopped (which flushes it) and closed, with a deadline (default 10 s). An exporter whose drain passes the deadline is cut: the undelivered events are counted as `drain` drops and the reload reports its name.
- A batch goes entirely to the old set or entirely to the new one. An event collected before the reload is delivered once to the old exporter or once to the new one, never twice and never lost.

## Shutdown

- `Shutdown(ctx)` stops accepting events, delivers what the channel holds, stops and flushes every manager once, and returns a report of what was drained and dropped.
- The deadline of `ctx` bounds the whole shutdown. Past it, the events still in the channel and the events an exporter did not deliver are dropped and counted.
- A second `Shutdown` does nothing and returns the first report. No goroutine of the package is left (a manager that was cut while still flushing ends when its flush returns).
- The end of the context given to `Start` triggers a shutdown.

## Notifiers

- One goroutine consumes the changes channel of the reconcile engine and offers each change to a **bounded queue per notifier** (default 64). Each notifier has one goroutine that calls it, one change at a time, in queue order: the changes of a flag set reach a notifier in order.
- A slow or failing notifier fills its own queue and loses its own changes (counted as `dropped`). It never delays the consumer or the other notifiers. There is no per-call timeout: the notifiers bound their own HTTP calls.
- A change without a diff is skipped. An error is counted and logged by class only (the message of a notifier error can carry its URL). A panic is recovered, logged and counted.
- A `Broadcast` function field is called with every change, before the notifiers, for the relay's SSE and websocket broadcast. It must not block.
- Reload and shutdown follow the same rules as for exporters: unchanged notifiers keep their goroutine and queue, removed ones deliver what they hold within the deadline.

## Supported kinds

| Section | Kinds |
| --- | --- |
| exporters | `file`, `webhook`, `log`, `s3`, `kinesis`, `sqs`, `redshift`, `googleStorage`, `kafka`, `pubsub`, `bigquery`, `azureBlobStorage`, `opentelemetry` |
| notifiers | `slack`, `microsoftteams`, `discord`, `webhook` |

- Defaults (`format`, `filename`, `csvTemplate`, `parquetCompressionCodec`, `logFormat`, `flushInterval`, `maxEventInMemory`) are those of version 1.
- `eventType: tracking` is not carried yet: such an exporter is rejected with a typed error.
- `googleCredentials` of `googleStorage` and `pubsub` is applied as a client option (version 1 ignored it for these kinds). `kafka` takes the topic and the addresses only.

### AWS kinds and connections

| Kind | Connection type | Taken from the connection |
| --- | --- | --- |
| `s3` | `s3` | the S3 client (`Factory.S3`), the bucket |
| `kinesis` | `kinesis` | the AWS configuration (`Factory.AWSConfig`), the stream (unless `streamName` or `streamArn` is set) |
| `sqs` | `sqs` | the AWS configuration, the queue URL (unless `queueUrl` is set) |
| `redshift` | `redshift` | the AWS configuration |

- Exporters sharing a connection share one client or configuration: the factory builds it once.
- An unknown connection, a connection of the wrong type and invalid settings are typed errors that name the entry (`exporters[2] "archive"`).

## Secrets

- Secrets are read from the resolved bootstrap secrets (`fromEnv`, `fromFile`).
- They never appear in logs, errors or the status. The error of an exporter is scrubbed of the secret values of its entry before it is logged.

## Metrics

Names and labels are closed. `exporter` and `notifier` are the names of the entries of the bootstrap file, `kind` their kind. There is no label per flag, per flag set or per event value. The collector (`events.NewCollector`) is registered by the relay when it switches to config v2; it is not part of the version 1 metrics.

| Metric | Type | Labels |
| --- | --- | --- |
| `semaphore_events_queue_capacity` | gauge | |
| `semaphore_events_queue_length` | gauge | |
| `semaphore_events_accepted_total` | counter | |
| `semaphore_events_dropped_total` | counter | `reason` (`queue_full`, `shutdown`, `drain`) |
| `semaphore_exporter_queued_events` | gauge | `exporter`, `kind` |
| `semaphore_exporter_enqueued_events_total` | counter | `exporter`, `kind` |
| `semaphore_exporter_exported_events_total` | counter | `exporter`, `kind` |
| `semaphore_exporter_dropped_events_total` | counter | `exporter`, `kind`, `reason` (`overflow`, `drain`) |
| `semaphore_exporter_flush_failures_total` | counter | `exporter`, `kind` |
| `semaphore_exporter_flush_duration_seconds` | summary (count and sum) | `exporter`, `kind` |
| `semaphore_exporter_state` | gauge | `exporter`, `kind`, `state` (`running`, `draining`, `stopped`, `cut`) |
| `semaphore_notifier_queued_changes` | gauge | `notifier`, `kind` |
| `semaphore_notifier_changes_total` | counter | `notifier`, `kind`, `result` (`delivered`, `error`, `panic`, `dropped`) |

The status snapshot (`Status()`) is immutable and read without blocking. Per exporter it has the name, the kind, the state, the queued, handed and exported counts, the drops by reason, and the class of the last error (`none`, `timeout`, `canceled`, `panic`, `other`).

## Not implemented yet

- A queue per exporter with a choice of overflow policy (`dropOldest`, `block` with a deadline).
- The tracking overhead criterion (tracking on within 5 % of tracking off) and its soak test.
- Wiring into the relay (`main.go`) and the evaluator.
- The `flagSets` selector of an exporter.
- Exporters for tracking events (`eventType: tracking`).
