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
rejected flags, and `segments` with the number of `static` and `dynamic` segments and, when the flag set has dynamic segments, `dynamicStates` with how many are `valid` (served) and how many are `rejected` (the connections cannot serve them: they answer `NOT_CONFIGURED`), left out when the flag set has none), `objects` (state, reason, checksum held and listed, rejected flags, and `kept`: the rejected flags still served from an earlier version of the object), `state` (the state directory: `enabled`, `lastSaveError`, `saves`, `saveErrors`, and `keptFlags`: the flags a restart would not restore), `withdrawn` and `pendingRestart`.
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
| `semaphore_resource_count` | `kind` (`flagset`, `object`, `dynamicSegment`), `state` | resources by state, always exported; `dynamicSegment` counts the dynamic segment definitions of the flag sets as `valid` or `rejected` |
| `semaphore_resource_status` | `kind`, `name`, `state` | 1 for a resource that is not ready, at most 100 per kind |
| `semaphore_config_pending_restart` | | settings that changed and need a restart |

The generation of the manifest the reader fetched (`semaphore_manifest_generation`: the engine may have rejected it; the generation served is `semaphore_config_generation{layer="applied"}`), the S3 requests and the exporters have their own metrics
(`semaphore_manifest_*`, `semaphore_s3_requests_total`, `semaphore_exporter_*`).

Cardinality: no label carries a flag or a key. The counts per state are computed when the runtime is swapped, not per
scrape, and a scrape evaluates no flag: its cost does not grow with the number of flags per flag set.
Per-flag-set series exist only for flag sets that are not ready.

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
