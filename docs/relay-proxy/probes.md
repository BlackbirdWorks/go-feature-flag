# Probes and observability

The HTTP server that `corehttp.New` builds on the relay core serves the probes below on the same listener as
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
rejected flags, and `segments` with the number of `static` and `dynamic` segments and, when the flag set has dynamic segments, `dynamicStates` with how many are `valid` (served), how many are `rejected` (the connections cannot serve them: they answer `NOT_CONFIGURED`), and among the valid ones how many are `healthy` and how many have a `storeError` now; left out when the flag set has none), `segmentStores` (each connection that can serve segments: `name`, `kind`, `state`: `idle`, `connecting`, `connected`, `error`, `closed`), `segmentBindings` (`live`: the shared bindings of the dynamic segments; `snapshots`: for each connection the held sets of the `snapshot` mode by state, `loaded`, `stale`, `missing`, `neverLoaded`, and their total `members`; left out when no dynamic segment is served), `objects` (state, reason, checksum held and listed, rejected flags, and `kept`: the rejected flags still served from an earlier version of the object), `state` (the state directory: `enabled`, `lastSaveError`, `saves`, `saveErrors`, and `keptFlags`: the flags a restart would not restore), `withdrawn` and `pendingRestart`.
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
| `semaphore_resource_count` | `kind` (`flagset`, `object`, `dynamicSegment`, `unresolvedSegment`), `state` | resources by state, always exported; `dynamicSegment` counts the dynamic segment definitions of the flag sets as `valid` or `rejected`; `unresolvedSegment` (state `unresolved`) counts the segment names that flags use and no segments object of their flag set defines |
| `semaphore_segment_lookups_total` | `connection`, `result` (`hit`, `miss`, `coalesced`, `short_circuited`, `unknown`) | lookups of the `lookup` mode by how the resolver answered them; `hit`, `miss` and `coalesced` are the cache outcomes |
| `semaphore_segment_lookup_duration_seconds` | `connection` | histogram of the duration of a resolver call (one batch of lookups; buckets 0.5ms to 5s) |
| `semaphore_segment_unknown_total` | `connection`, `reason` (`none`, `store`, `circuit_open`, `waiting`, `not_loaded`, `oversized`, `invalid`, `unanswered`, `overdue`) | lookups that stayed unknown, by why (the first 8 segments of a call are reported) |
| `semaphore_segment_store_calls_total` | | calls of the resolvers to the stores |
| `semaphore_segment_store_failed_calls_total` | | those calls that failed |
| `semaphore_segment_breaker_transitions_total` | `to` (`open`, `half_open`, `closed`) | transitions of the circuit breakers |
| `semaphore_segment_breaker_state` | `connection`, `state` (`open`, `half_open`) | segments whose circuit breaker is in that state now; closed is the rest |
| `semaphore_segment_named_lookups_total` | `segment`, `result` (`hit`, `miss`, `coalesced`, `unknown`) | opt-in (`observability.metrics.perSegmentLabels`): lookups by segment name |
| `semaphore_segment_named_unknown_total` | `segment`, `reason` | opt-in: unknown lookups by segment name and reason |
| `semaphore_segment_named_overflow_total` | | opt-in: observations of segments beyond `maxSegmentLabels`, counted in `segment="other"` |
| `semaphore_segment_store_state` | `connection`, `state` | 1 for the state of each store connection, 0 for the others |
| `semaphore_segment_dynamic` | `status` (`healthy`, `store_error`) | served dynamic segments by health now |
| `semaphore_segment_snapshot_sets` | `connection`, `state` (`loaded`, `stale`, `missing`, `never_loaded`) | held sets of the `snapshot` mode by connection and state now |
| `semaphore_segment_snapshot_members` | | members held by all the sets of the `snapshot` mode |
| `semaphore_segment_bindings` | | shared bindings of dynamic segments that the served runtime uses (one for each segments object and connection) |
| `semaphore_resource_status` | `kind`, `name`, `state` | 1 for a resource that is not ready, at most 100 per kind |
| `semaphore_config_pending_restart` | | settings that changed and need a restart |
| `semaphore_flag_rejected_total` | `reason` (`kept`, `dropped`) | flags rejected at load: `kept` serves the last valid definition, `dropped` had none |
| `semaphore_flags_rejected` | `reason` (`kept`, `dropped`) | rejected flags present in the running generation (a gauge: it falls when the rejection is fixed; alert on it for "still rejected") |
| `semaphore_object_status` | `state` (`ok`, `stale`, `missing`, `pending`, `invalid`) | source objects by state, always exported |
| `semaphore_object_oldest_unapplied_age_seconds` | | seconds since the oldest object that is not `ok` left that state, 0 when all are `ok` |
| `semaphore_config_summaries_total` | | status summaries built: one per swapped runtime, by the first probe or scrape that reads it (readers that meet it together share one build) |

Cardinality: the segment series are labelled by `connection` (a name of the bootstrap file) and by closed lists (`result`, `reason`, `state`), never by a flag set, member, key or id. The series by segment name are off unless `observability.metrics.perSegmentLabels` is `true`; the names that have a series are the live dynamic segments of the served generation (a name that is gone loses its series and frees its slot), at most `maxSegmentLabels` of them (default 100) in sorted order, and every other name is counted under `segment="other"` (a segment that is itself named `other` shares it); a series of a name that comes back starts at zero. The series cost one atomic load and atomic adds per segment, no lock. The `snapshot` mode answers from memory without the resolver, so the resolver series cover the `lookup` mode only; `semaphore_segment_snapshot_*` cover the snapshot mode. A request that reaches dynamic segments writes one span `segmentLookups` when tracing is on (`observability.tracing.otlpEndpoint`), with `semaphore.segment.connections`, `semaphore.segment.lookups`, `semaphore.segment.unanswered` and `semaphore.segment.result` (`answered`, `unanswered`, `failed`) and no id. The span is on exactly when tracing is (`opentelemetry.Enabled`, the predicate that starts the exporter). The evaluator has no request context yet, so it is a root span, not a child of the request span: it is recorded only with a sampler that samples roots (`always_on`, `traceidratio`), not with the default `parentbased_always_off`.

Health of a store that nothing has contacted: a connection that is configured but was never asked (a `lookup` segment with no `version.key`, and no request has reached it yet) reads healthy in `/info` and in `semaphore_segment_dynamic` until the first lookup fails; a `snapshot` segment, and a `lookup` segment with a `version.key`, are contacted by the refresh loop and show `store_error` within one interval. A cheap fix would be one probe of the store (a `PING`) when a connection is first opened; it is not built.

The generation of the manifest the reader fetched (`semaphore_manifest_generation`: the engine may have rejected it; the generation served is `semaphore_config_generation{layer="applied"}`), the S3 requests and the exporters have their own metrics
(`semaphore_manifest_*`, `semaphore_s3_requests_total`, `semaphore_exporter_*`).

Rejected flags: `semaphore_flag_rejected_total` counts a rejected flag **once**, in the applied generation whose content
brought the rejection (a new version of its object, or a flag that was valid before). A poll or a generation that leaves
the rejection as it was counts nothing, so the counter never grows with the poll interval; `rate()` shows new rejections.
The rejections still present are the gauge `semaphore_flags_rejected` and, per flag set and object, `rejected` and `kept` in
`/info` (`semaphore_object_status` counts objects by state: an object with a rejected flag is still `ok`). The flag set is not a
label: the flag sets that have a problem are in `semaphore_resource_status`. Object names appear only in
`semaphore_resource_status{kind="object"}` (not `ok` only, at most 100): there is no per-object series for every object.
The age starts when this process first saw the object not `ok`, and a restart from the state directory starts it again.

The age is the only value computed per scrape: each scrape subtracts the recorded start time from the current time. The
other values are computed when a generation is built, not per scrape, and nothing was added to the request path.

Cardinality: no label carries a flag or a key. The counts per state are computed once per swapped runtime, by the first read
(`semaphore_config_summaries_total` counts them; probes and scrapes that meet it together wait for the one build), not per
scrape, and a scrape evaluates no flag: its cost does not grow with the number of flags per flag set.
Per-flag-set series exist only for flag sets that are not ready.

## Reload log line

Every reload writes one `runtime reload` line (`log/slog`), and an unchanged poll writes none:

- Applied (level info): `outcome=applied`, `generation`, `snapshot`, `flagsets.created|updated|deleted`,
  `flags.created|updated|deleted`, `objects.ok|stale|missing|pending|invalid`, `rejected` (flags rejected in the
  objects that run) and `duration`.
- Rejected (level warn): `outcome=rejected`, `generation`, the stable `code` and the `detail`. The running generation
  stays (last known good). `detail` is free text and can quote the content of an object, as the page says for `/info`.

The lines carry counts, codes and paths, and never an API key or an admin key. Only the `detail` of a rejected line can
quote object content.

## Admin key equal to a flag-set key

Flag-set keys are given to clients and are not secrets, so such a key must never become an admin credential. A key that is
both an admin key and an API key of a flag set therefore grants **nothing** while the collision exists. The generation is
still applied (a published key can never make the relay unready or stuck), and:

- the key is not routed to the flag set, and it is not accepted as an admin key: both answer `401`, as for an unknown key,
  with no hint in the response;
- the other keys of the flag set and the other admin keys work;
- the flag set reports the problem `admin-key-collision` in `/info` (state `degraded`, fewer `apiKeys`; a pending flag
  set stays `pending`) and in `semaphore_resource_status`, the way `duplicate-key` is reported for a key held by two flag
  sets;
- `/info` shows `config.disabledAdminKeys`, the number of admin keys that are disabled this way;
- one `ERROR` line is logged where the collision is new, saying that an admin key is disabled because a flag set
  publishes the same key. It names the flag set and never the key.

If every admin key is disabled this way the relay still serves flags and stays ready; the admin endpoints answer `401`
until the bootstrap file or the published document is fixed.

The routing is derived again from the content the relay holds each time the admin keys change, so a reload of the
bootstrap file (`SIGHUP`) that rotates the admin key makes the flag-set key route again, and a reload that introduces a
collision disables the key, without publishing anything. The check is made when a generation is built or the admin keys
change; evaluation requests do no extra work (the routing table simply does not hold the key).

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
