# Store request budget

The relay reads the control plane with a small, fixed number of store requests. The number depends on the poll
interval and the number of replicas. It does not depend on the number of flags, flag sets, API keys, source objects or
buckets.

## Rules

- Each replica sends one conditional `GET` of the manifest per poll interval for each manifest (`C`, normally 1). The
  store answers `304 Not Modified` when nothing changed. Nothing else is sent: no `LIST`, no `HEAD`, no object `GET`.
- A new generation costs the manifest `GET` (it answers `200`) plus one `GET` for each source object whose checksum
  differs from the held one (`K`). Each replica fetches a changed object once, whatever the number of flag sets that
  list it. A replica waits a random delay of up to 5 s before it fetches, so replicas do not fetch at the same instant.
- A start with no saved state costs the manifest `GET` plus one `GET` for each listed object (`N`), 4 at a
  time by default (16 at most). A start with a current [state directory](../relay-proxy/bootstrap-config.md#controlplane) costs the
  conditional manifest `GET` only.
- A reload of the bootstrap file that changes nothing in the control plane costs no request.
- A `304` is billed by the store as a `GET`. It is small on the wire.

The default poll interval is 60 s (`controlPlane.manifest.pollInterval`, or `poll.interval` of the connection). It
varies by +/-10 % (`jitter`), so the numbers below are means.

## Inputs

<!-- budget: inputs -->
| Input | Value |
|---|---|
| Poll interval in seconds (`I`) | 60 |
| Minutes in a month (30 days) | 43200 |
| Price of 1000 `GET` requests in USD | 0.0004 |

The price is the list price of S3 Standard `GET` requests at the time of writing. Check the current price of your
provider.

## Steady state

A replica sends `60 / I` requests per minute, that is 1 at the default interval. The fleet sends `R` times that.

<!-- budget: fleet -->
| Replicas (`R`) | Requests per minute | Requests per day | Requests per month | Cost per month in USD |
|---|---|---|---|---|
| 1 | 1 | 1440 | 43200 | 0.02 |
| 10 | 10 | 14400 | 432000 | 0.17 |
| 50 | 50 | 72000 | 2160000 | 0.86 |
| 200 | 200 | 288000 | 8640000 | 3.46 |

The same numbers for one replica at other intervals. Halving the interval doubles every number.

<!-- budget: interval -->
| Poll interval in seconds | Requests per minute | Requests per month |
|---|---|---|
| 30 | 2 | 86400 |
| 60 | 1 | 43200 |
| 120 | 0.5 | 21600 |
| 300 | 0.2 | 8640 |

## A generation change

The fleet sends `R x K` object requests once, on top of the manifest requests that the poll sends anyway (the `200`
replaces one `304` per replica).

<!-- budget: change -->
| Replicas (`R`) | Changed objects (`K`) | Object requests, once |
|---|---|---|
| 10 | 1 | 10 |
| 10 | 5 | 50 |
| 200 | 1 | 200 |

## Start of a replica

<!-- budget: start -->
| Situation | Requests per replica | With `C` = 1 and `N` = 5 |
|---|---|---|
| No saved state | `C + N` | 6 |
| Current state directory | `C` | 1 |

## Check it with the request metric

`semaphore_s3_requests_total{connection,operation,result}` counts every attempt of every request of a connection. In
steady state the series `operation="get",result="not_modified"` of the control-plane connection grows by one per
interval and replica, and no other series of that connection grows. After a generation change the series
`result="ok"` grows by `1 + K` once per replica. A request that the store client retries counts once for each attempt.
See [the polling page](control-plane-polling.md#request-metric) for the labels.

## Pickup latency

The time from a publish to the new content on a replica, for a poll that works, is at most the interval plus the
jitter plus the fetch delay, before the download. A failed request or a retry makes it longer.

<!-- budget: latency -->
| Part | Seconds |
|---|---|
| Poll interval | 60 |
| Jitter (10 %) | 6 |
| Fetch delay (at most) | 5 |
| Total before the download | 71 |
