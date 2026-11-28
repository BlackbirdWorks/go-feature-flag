# Runbook

For each case: what you see, where to look, and what to do. Every metric, field and log message here exists in the
code, and a test checks it. The metrics are on the [probes page](probes.md#metrics), the fields of `/info` on the same
page.

Where a step needs the text of a failure, read the logs: `/info` shows closed codes only.

## The store is not reachable

The relay keeps serving the last good content. `/readyz` stays ready, see the [readiness rules](probes.md#readiness-rules).

| Where | What to look for |
|---|---|
| Metric | `semaphore_config_age_seconds` grows. `semaphore_s3_requests_total` shows `result` values other than `ok` and `not_modified`. `semaphore_manifest_backoff_failures` is above 0. |
| `/info` | `config.staleness.ageSeconds` grows, `config.staleness.lastConfirmed` stops moving. |
| Log | `"manifest reader: manifest poll failed"` with the closed reason (`denied`, `timeout`, `error`). |

What to do:

- Fix the store, the credentials or the network. The relay polls again by itself, with a growing delay that is capped
  (5 minutes), and recovers without a restart.
- Alert on `semaphore_config_age_seconds`, not on `/readyz`.
- A replica that restarts during the outage serves the saved state at once when `controlPlane.stateDir` is set. Without
  it the replica waits: `/readyz` answers `waiting-for-manifest`.

## An object is corrupted or missing

Only the flag sets that list the object are affected. The object keeps its last valid content.

| Where | What to look for |
|---|---|
| Metric | `semaphore_object_status{state="stale"}` or `{state="missing"}` is above 0. `semaphore_object_oldest_unapplied_age_seconds` grows. |
| `/info` | `config.objects[]` has `state` `stale` or `missing`, with `reason` `checksum-mismatch`, `size-mismatch`, `missing` or `too-large`, and the listed and held checksum. The flag set is `degraded` in `config.flagSets`. |
| Log | `"manifest reader: object not updated"`, and `"manifest reader: object recovered"` when it works again. |

What to do:

- `checksum-mismatch`: the stored bytes are not the ones the manifest lists. Write the object again from the source and
  publish. An object key is content addressed, so never overwrite a key with other content.
- `missing`: the object is not in the store. Upload it, or publish a manifest without it.
- The relay retries a failed object with a growing delay (up to 5 minutes), so it recovers by itself when the store is
  fixed. A new generation tries every failed object at once.

## A generation is rejected

The relay keeps the held generation. Two components can reject, and each leaves its trace in another place.

The reader rejects a manifest that is not valid, is of another channel or has a lower generation:

| Where | What to look for |
|---|---|
| `/info` | `config.reader` has `outcome`, `reason` and `at`. |
| Metric | `semaphore_config_reader_rejections_total` grows for that `reason` (same closed set as `config.reader.reason`); `semaphore_config_generation{layer="applied"}` does not move while the publisher has written a newer generation. |
| Log | `"manifest reader: manifest rejected, previous one kept"` at level warn, with the attributes `outcome` and `reason`. |

Outcomes: `invalid`, `lower-generation`, `generation-conflict`, `channel-mismatch`. The reason for `invalid` is a
closed code such as `unsupported-version` or `wrong-kind`. Read the log or `config.reader` for the outcome and reason.

The engine rejects content that the reader accepted but that does not build:

| Where | What to look for |
|---|---|
| Metric | `semaphore_config_reload_total{result="rejected"}` grows. `semaphore_manifest_generation` is ahead of `semaphore_config_generation{layer="applied"}`. |
| `/info` | `config.lastRejected` has `code`, `generation` and `at`. |
| Log | `"runtime reload"` at level warn with `outcome=rejected` and the `code`. |

Codes of a rejected round: `invalid-content`, `stale-generation`, `build-failed`, `vetoed`.

What to do:

- `lower-generation` or `stale-generation`: the publisher wrote a generation that is not higher than the held one.
  Publish again with a higher generation.
- `generation-conflict`: two writers published the same generation with other content. Publish a higher generation.
- `invalid` or `invalid-content`: read the `reason` or `detail` in the log, fix the document, publish a new generation.
- Validate before you publish with the `controlplane` package; `semaphore-cli publish` does it.

## Unknown fields in control-plane documents

Accepted documents with fields from a newer schema or misspelt field names are not rejected, and their unknown fields are counted:

| Where | What to look for |
|---|---|
| `/info` | `config.unknownFields` lists each document `kind` (`flagSets`, `flags`, `manifest`, `segments`) with `count` and the first `fields` paths when non-zero. |
| Metric | `semaphore_config_unknown_fields` gives the count per `kind`. |
| Log | `"unknown fields in control-plane documents"` at level warn when the count for a kind changes above zero, and `"unknown fields cleared in control-plane documents"` when it returns to zero. |

What to do:

- Run `semaphore-cli config validate` or `semaphore-cli publish verify` against the published files to locate and fix misspelt keys or confirm whether the relay needs an upgrade for a newer schema field.
- Note: flags objects in TOML format are not inspected for unknown fields and report 0.

## `pendingRestart` does not go away

A setting of the bootstrap file changed, and the relay does not apply it in place.

| Where | What to look for |
|---|---|
| Metric | `semaphore_config_pending_restart` is above 0. |
| `/info` | `config.pendingRestart` lists the names of the settings (never their values). |
| Log | `"reload queued"` after `SIGHUP`, then `"runtime reload"`. |

What to do:

- Restart the process. A reload cannot clear the list, because the settings it names apply only at the start.
- If the change was a mistake, put the old value back and send `SIGHUP`: the list is the difference with the file the
  process started with, so it empties.
- The table of settings that apply live is in [the bootstrap file reference](bootstrap-config.md#reload-of-each-setting).

## Rotate an API key

A flag set can hold several keys, so a key rotates without a gap. A replica applies a published document at its next
poll, so replicas change at different times.

1. Add the new key to the flag-set document and publish. Note the generation that the command prints.
2. Wait until every replica serves that generation: `config.generation` of `/info` (or
   `semaphore_config_generation{layer="applied"}`) is at least the published one on each replica. A replica that has not
   applied it answers `401` to the new key.
3. Move the clients to the new key. Keep the old key in the document until every client has moved.
4. Remove the old key from the document and publish. The old key answers `401` from that generation.
5. Check `config.flagSets[].apiKeys` (the number of keys) and that `config.flagSets[].problems` is empty. A key that two
   flag sets hold is reported as `duplicate-key`, and the later flag set loses it.

An admin key rotates in the bootstrap file: add the new reference, send `SIGHUP`, remove the old one, send `SIGHUP`.
A key that is both an admin key and a flag-set key is disabled: `config.disabledAdminKeys` counts these.

## Roll back

Publish the earlier content again. The relay never goes back to a lower generation, so a rollback is a new generation
with the old content.

1. Check out the source of the version you want.
2. Run `semaphore-cli publish`. Objects with the same bytes have the same key and checksum, so they are not written
   again. The manifest gets the next generation.
3. Check `config.generation` on the replicas, and `config.lastApplied`.

Do not lower `generation` by hand: the relay ignores the manifest (`lower-generation`).

## The state directory does not save

| Where | What to look for |
|---|---|
| Metric | `semaphore_manifest_state_saves_total{result="error"}` grows. |
| `/info` | `config.state.lastSaveError` is not empty, and `config.state.saveErrors` grows. |
| Log | `"state directory: save failed, polling goes on"`. |

The relay keeps serving. Fix the disk or the permissions of the directory ([security](security.md#state-directory)).
At the start, `"state directory: manifest copy is corrupt, starting empty"` says the saved copy was not used and the
relay read the store.

## Segment store outages

A dynamic segment reads a Valkey or Redis store. The relay stays ready while the store fails, and the evaluations answer by the [table of the outage policy](../configure-flags/segments-dynamic.md#what-happens-when-the-store-is-down). The chaos tests (`internal/loadtest/segments`) run each case below against a real store and a real relay in the pull request checks, and one test fails when a row of that table is added, removed or renamed without them.

| Symptom | Test | Where to look |
|---|---|---|
| Lookups answer `STORE_ERROR`, a loaded snapshot answers `STALE` | `TestChaos_KillStore` (the container stops, then starts again) | Metric: `semaphore_segment_dynamic{status="store_error"}` and `semaphore_segment_store_state`. `/info`: `config.segmentStores[].state` and `config.flagSets[].segments.dynamicStates.storeError`. Log: `"a segment store fails"` once, `"a segment store works again"` once. |
| Lookups are short-circuited without a call to the store | `TestChaos_KillStore` | Metric: `semaphore_segment_breaker_state{state="open"}` is above 0 and `semaphore_segment_lookups_total{result="short_circuited"}` grows. The breaker closes by itself after its cooldown when the store is back. |
| The store accepts connections and never answers (a hung node, a firewall that drops) | `TestChaos_PauseStore` (`docker pause`) | Every evaluation still ends at its lookup timeout plus a small margin. Metric: `semaphore_segment_unknown_total{reason="store"}` grows, `semaphore_segment_lookup_duration_seconds` sits at the timeout. Snapshots turn `STALE` after three poll intervals. Log: `"a segment store fails"`. |
| A slow store | `TestChaos_AddedLatencyAndCut` | Below the timeout the answers are right and `semaphore_segment_lookup_duration_seconds` shifts; above it the lookups are `STORE_ERROR` and `semaphore_segment_unknown_total{reason="store"}` grows. Raise `lookupTimeout` only if the store is slow by design. |
| The first requests after a start answer `STORE_ERROR` | `TestChaos_StoreDownAtStart`, `TestColdStart_FirstRequestsOnAHealthyStore` | `config.segmentStores[].state` is `connecting` or `error`: the store cannot be reached from this relay. A healthy store is dialled when the first generation binds the segments. |
| One snapshot is `STALE` or empty, the others are fine | `TestChaos_SnapshotSetDeletedOrBroken` | Metric: `semaphore_segment_snapshot_sets{state="stale"}` or `{state="missing"}`. A set that was deleted is "nobody"; a key of the wrong type keeps the last good set (`STALE`). |
