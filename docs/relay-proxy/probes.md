# Probes and observability of the config v2 runtime

The HTTP server that `corehttp.New` builds on a config v2 core serves the probes below on the same listener as
`/health` and `/info` (the monitoring listener when there is one, else the API listener). They need no API key and never
contain a key, a secret or the bytes of an object. The relay binary serves them on its API listener, or on its `ops` listener when it has one.

## Endpoints

| Endpoint | 200 | 503 |
| --- | --- | --- |
| `/livez` | `{"live": true}` while the process serves. Never depends on the control plane or a flag set. | never |
| `/readyz` | `{"ready": true, "state": "empty"}` (or `serving`, `degraded`) | `{"ready": false, "reason": "starting"}` (or `waiting-for-manifest`, `shutting-down`) |
| `/startupz` | `{"ready": true}` once the first bootstrap round has ended | `{"ready": false, "reason": "starting"}` |
| `/health` | `{"initialized": true}`, always, as in v1 | never |
| `/info` | the v1 fields plus a `config` section | never |

## Readiness rules

- The relay is **not ready** until the first round of the engine that carried the bootstrap file has ended, whatever its
  outcome (`starting`; a file with no admin keys swaps nothing, and still counts) and the first manifest outcome is
  known (`waiting-for-manifest`): a manifest applied, even one that lists zero objects, or the verified copy of the state
  directory. Without a manifest and without a state copy the relay retries with backoff and stays up.
- Once ready the relay **stays ready** through any later S3 or manifest outage and serves the last known good content.
  Staleness shows in `/info` and the metrics, never in `/readyz`.
- A relay with zero flag sets is ready (`empty`). A stale or missing object, a rejected flag, or a flag set that is
  degraded or pending makes the state `degraded`, never unready.
- With a state directory a restart during an outage is ready at once.
- `shutting-down` is answered first on shutdown, before the listeners close, and stays until the process exits; the
  relay keeps serving during the drain delay, see [Shutdown in Kubernetes](#shutdown-in-kubernetes).

Reasons (closed list): `starting`, `waiting-for-manifest`, `shutting-down`. States of a 200: `empty`, `serving`, `degraded`.

## `/info` config section

`config` holds: `generation` (applied manifest generation), `snapshot`, `lastReload` (the most recent round of any
outcome), `lastApplied` and `lastRejected` (outcome, code, generation, time), `reloads` (rounds by outcome), `staleness` (last confirmation by the control plane, last successful
reload, age in seconds), `flagSets` (state `ready`, `degraded` or `pending`, problems, flags, number of API keys,
rejected flags, and `segments` with the number of `static` and `dynamic` segments, left out when the flag set has none), `objects` (state, reason, checksum held and listed, rejected flags, and `kept`: the rejected flags still served from an earlier version of the object), `state` (the state directory: `enabled`, `lastSaveError`, `saves`, `saveErrors`, and `keptFlags`: the flags a restart would not restore), `withdrawn` and `pendingRestart`.
The free text of a failure (the detail of a round, the text of a problem) is not served, because it can quote the content
of an object: `/info` shows closed codes, and the text is in the logs. `lastRejected` stays until a later applied round supersedes it, then it is absent; ignored (unchanged) rounds do not clear
it. It works with zero flag sets.

## Metrics

| Metric | Labels | Meaning |
| --- | --- | --- |
| `semaphore_config_generation` | `layer` (`applied`, `received`) | generation served, and the last generation handed to the engine |
| `semaphore_config_snapshot` | | local swap counter |
| `semaphore_config_reload_total` | `result` (`applied`, `rejected`, `ignored`) | reconcile rounds |
| `semaphore_config_last_success_timestamp_seconds` | | Unix time of the last applied round, 0 when none |
| `semaphore_config_age_seconds` | | seconds since the control plane last confirmed the manifest |
| `semaphore_resource_count` | `kind` (`flagset`, `object`), `state` | resources by state, always exported |
| `semaphore_resource_status` | `kind`, `name`, `state` | 1 for a resource that is not ready, at most 100 per kind |
| `semaphore_config_pending_restart` | | settings that changed and need a restart |
| `semaphore_flag_rejected_total` | `reason` (`kept`, `dropped`) | flags rejected at load: `kept` serves the last valid definition, `dropped` had none |
| `semaphore_object_status` | `state` (`ok`, `stale`, `missing`, `pending`, `invalid`) | source objects by state, always exported |
| `semaphore_object_oldest_unapplied_age_seconds` | | seconds since the oldest object that is not `ok` left that state, 0 when all are `ok` |

The generation of the manifest the reader fetched (`semaphore_manifest_generation`: the engine may have rejected it; the generation served is `semaphore_config_generation{layer="applied"}`), the S3 requests and the exporters have their own metrics
(`semaphore_manifest_*`, `semaphore_s3_requests_total`, `semaphore_exporter_*`).

Rejected flags: `semaphore_flag_rejected_total` counts a rejected flag **once**, in the applied generation whose content
brought the rejection (a new version of its object, or a flag that was valid before). A poll or a generation that leaves
the rejection as it was counts nothing, so the counter never grows with the poll interval; `rate()` shows new rejections,
and the rejections still present are `rejected` and `kept` in `/info` and `semaphore_object_status`. The flag set is not a
label: the flag sets that have a problem are in `semaphore_resource_status`. Object names appear only in
`semaphore_resource_status{kind="object"}` (not `ok` only, at most 100): there is no per-object series for every object.
The age starts when this process first saw the object not `ok`, and a restart from the state directory starts it again.

All of this is computed when a generation is built, not per scrape, and nothing was added to the request path.

Cardinality: no label carries a flag or a key. The counts per state are computed when the runtime is swapped, not per
scrape, and a scrape evaluates no flag: its cost does not grow with the number of flags per flag set.
Per-flag-set series exist only for flag sets that are not ready.

## Reload log line

Every reload writes one `runtime reload` line (`log/slog`), and an unchanged poll writes none:

- Applied (level info): `outcome=applied`, `generation`, `snapshot`, `flagsets.created|updated|deleted`,
  `flags.created|updated|deleted`, `objects.ok|stale|missing|pending|invalid`, `rejected` (flags rejected in the
  objects that run) and `duration`.
- Rejected (level warn): `outcome=rejected`, `generation`, the stable `code` and the `detail`. The running generation
  stays (last known good).

The lines carry counts, codes and paths. They never carry an API key, an admin key or the content of a flag.

## Admin key equal to a flag-set key

A key that is an admin key and an API key of a flag set would give a flag-set client the admin endpoints, so the relay
refuses it. The admin keys come with the bootstrap file and the flag-set keys with the published documents, so the check
runs when the generation is built. The generation is rejected with the code `admin-key-collision`, the running generation
stays, and `/info` shows it in `lastRejected` and `lastReload`. The detail names the flag set by path
(`flagSets[name].apiKeys`) and never prints a key. A `SIGHUP` that adds such an admin key is rejected the same way.
(A key held by two flag sets is different: the flag set that already holds it keeps it and the other reports
`duplicate-key`.)

## Kubernetes

```yaml
startupProbe:
  httpGet: {path: /startupz, port: 1031}
  periodSeconds: 2
  failureThreshold: 60
livenessProbe:
  httpGet: {path: /livez, port: 1031}
  periodSeconds: 10
readinessProbe:
  httpGet: {path: /readyz, port: 1031}
  periodSeconds: 5
```

Use `/livez` for the liveness probe: it never fails because the control plane is down, so a storage outage does not
restart the relay.

## Shutdown in Kubernetes

On `SIGTERM` the relay fails `/readyz` (503 `shutting-down`) and keeps serving for `server.shutdown.drainDelay`, so that
the endpoint controllers and load balancers stop sending traffic before the listeners close. Then it stops within
`server.shutdown.timeout` (default `15s`); the order is in the [bootstrap file page](bootstrap-config.md#shutdown).

- Set `terminationGracePeriodSeconds` to at least `drainDelay + timeout` plus a margin (for example 10 seconds), or the
  kubelet sends `SIGKILL` before the relay finishes. With `drainDelay: 10s` and the default timeout:
  `terminationGracePeriodSeconds: 35`.
- A `preStop` hook is not needed: the drain delay does what a `sleep` in `preStop` does, and the readiness probe already
  fails.
- A shutdown that ends in the hard stop exits with status 1.

```yaml
spec:
  terminationGracePeriodSeconds: 35
  containers:
    - name: semaphore
      env:
        - {name: SEMAPHORE_SHUTDOWN_DRAIN_DELAY, value: "10s"}
```
