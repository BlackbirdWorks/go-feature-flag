# Reload semantics

A change of the served content never needs a restart and never drops a request. This page states what the relay
guarantees. The mechanism is on the [live reload page](live-reload.md).

## What triggers a reload

- A new manifest generation, found by the poll (see the [request budget](request-budget.md)).
- The bootstrap file, read again on `SIGHUP`. Only `auth.adminKeys` applies in place. Every other change waits for a
  restart and is listed in `config.pendingRestart` of `/info`. The table of settings is in the
  [bootstrap file reference](../relay-proxy/bootstrap-config.md#reload-of-each-setting).

## What is atomic

- The relay builds the new state off to the side and swaps it with one atomic pointer store. A request reads the
  state once and keeps it until it ends. It sees the old state or the new one, never a mix.
- A request in flight when the swap happens finishes on the state it started with. No request fails because of a swap.
- Flags, flag sets, API keys and the routing of keys change in the same swap. A key and its flag set never disagree.
- A reload that fails to build changes nothing. The running state stays.
- Reads take no lock.

## What one round does

1. It validates the content (generation, object states, formats).
2. It plans the change by object name and checksum.
3. It builds the new state and swaps it.
4. It reports the round: one `"runtime reload"` log line and the `config.lastReload` field of `/info`.

An unchanged poll writes no log line and swaps nothing.

## Per-level fallback

The levels and what each keeps are in [last known good](manifest-and-documents.md#last-known-good). In short:

- A manifest with an unsupported `apiVersion`, wrong `kind`, decode/validation failure (other than an object's content `version`), another channel, or a lower generation is rejected whole by the reader, before the
  engine sees it. No round runs, so `config.lastRejected` does not change. The reader writes one `"manifest reader:
  manifest rejected, previous one kept"` log line with the attributes `outcome` and `reason`, and the held state keeps
  serving. `/info` does not show the outcome.
- An object entry whose content `version` is higher than the reader supports (`unsupported-version`) does not reject
  the manifest: other objects apply normally, while that object keeps its last valid content (`stale`, or `missing` if
  never loaded) with `reason: unsupported-version`, and dependent flag sets become `degraded` (`source-degraded`) and keep serving their last-known-good content.
- A manifest at the same generation as the held one is not always ignored. The same objects in the same states change
  nothing (an unchanged poll). A `304` poll can still retry an object that failed: when it becomes readable, the reader
  publishes its new state at the same generation, and the engine applies it. Only another set of objects or another
  checksum at the same generation is a conflict (`generation-conflict`).
- When the engine rejects the content that the reader handed over (code `invalid-content`, `build-failed` or `vetoed`),
  the round is `rejected` and the code is in `config.lastRejected.code`. The held state keeps serving.
- An object that fails keeps its last valid content. `/info` lists it in `config.objects` with a `state` and a `reason`,
  and `semaphore_object_status` counts it.
- A flag that fails keeps its last valid definition. `config.objects[].kept` counts these flags, and
  `semaphore_flags_rejected` counts them in the running generation.

## A removed flag set

A flag set that a valid generation no longer defines leaves the routing table in that generation. Its API keys answer
`401`, as any unknown key does, and are free for another flag set at once. There is no last known good for a removed
flag set. `config.withdrawn` lists its name and the generation that removed it, never its keys.

## A flag set that is not available

A valid key whose flag set has no usable definition yet (it is new and its objects are `pending`) gets the answer of an
empty flag set: an OFREP single evaluation answers `404` with `FLAG_NOT_FOUND`, a bulk evaluation answers `200` with no flags. The flag
set is `pending` in `config.flagSets` until its content arrives.

## What a reader sees in the status

| Field of `/info` | Meaning |
|---|---|
| `config.generation` | The generation that is served. |
| `config.snapshot` | A counter of swaps in this process. |
| `config.lastReload` | The last round, whatever its outcome. |
| `config.lastApplied` | The last applied round. |
| `config.lastRejected` | The last rejected round, until a later round is applied. |
| `config.staleness` | When the control plane last confirmed the manifest, and the age. |

The free text of a failure is in the logs only, because it can quote object content.
