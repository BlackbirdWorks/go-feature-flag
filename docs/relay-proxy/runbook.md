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

The relay keeps the held generation.

| Where | What to look for |
|---|---|
| Metric | `semaphore_config_reload_total{result="rejected"}` grows. `semaphore_manifest_generation` is ahead of `semaphore_config_generation{layer="applied"}`. |
| `/info` | `config.lastRejected` has `code`, `generation` and `at`. |
| Log | `"runtime reload"` at level warn with `outcome=rejected` and the `code`; `"manifest reader: manifest rejected, previous one kept"` with the outcome. |

Codes of a rejected round: `invalid-content`, `stale-generation`, `build-failed`, `vetoed`. Outcomes of a manifest
the reader rejects: `invalid`, `lower-generation`, `generation-conflict`, `channel-mismatch`.

What to do:

- `lower-generation` or `stale-generation`: the publisher wrote a generation that is not higher than the held one.
  Publish again with a higher generation.
- `generation-conflict`: two writers published the same generation with other content. Publish a higher generation.
- `invalid` or `invalid-content`: read `detail` in the log, fix the document, publish a new generation.
- Validate before you publish with the `controlplane` package; `semaphore-cli publish` does it.

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

A flag set can hold several keys, so a key rotates without a gap.

1. Add the new key to the flag set document and publish. Clients start to use it.
2. Remove the old key from the document and publish. The old key answers `401` from that generation.
3. Check `config.flagSets[].apiKeys` (the number of keys) and that `config.flagSets[].problems` is empty. A key that two flag sets
   hold is reported as `duplicate-key`, and the later flag set loses it.

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
