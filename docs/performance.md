# Performance

Measured throughput and latency of the relay proxy for single flag evaluations and bulk evaluations, over HTTP/1.1
and HTTP/2 cleartext (h2c). The numbers come from the soak harness in `internal/loadtest`, so you can repeat them.
Treat them as a comparison between the modes on one machine, not as a capacity promise: the client and the server
run in the same process on the same CPUs.

## Setup

| | |
|---|---|
| CPU | Intel Core i7-7700K, 4 cores / 8 hardware threads (8 logical CPUs, `GOMAXPROCS=8`), 30 GB RAM |
| OS, Go | Linux 7.0, Go 1.27.4 |
| Load average before the run | 0.92 (one minute); about 4 to 6 during the runs |
| Server | the relay proxy in process (`internal/loadtest/relay`), file retriever, 48 flags, `exporter: discard` (webhook sink that answers 200) |
| Client | `net/http` client in the same process, N worker goroutines in a closed loop (each sends the next request when the previous answered), 10 attributes in the evaluation context, 1000 distinct users |
| Protocols | HTTP/1.1 with keep-alive, or h2c with prior knowledge (`server.enableH2C: true`; the client has no HTTP/1.1 fallback and every response is checked to use the expected protocol) |
| Duration | 40 s per run, the first 10 s (two samples) are the warm-up, the table shows the mean of the remaining 25 to 30 s |
| Tracking | on for the single flag endpoints (`rule-bool-track`: every evaluation queues an event, which the exporter discards); the bulk endpoints do not queue events |

Reproduce one cell:

```shell
go run ./internal/loadtest/cmd/soak -target relay -endpoint ofrep-bulk -protocol h2c \
  -workers 64 -exporter discard -duration 40s -interval 5s -warmup 2
```

Limitations, please read before quoting a number:

- Client and server share the CPUs (and the Go scheduler and garbage collector). Absolute numbers would be higher with the load
  generator on another machine, and the latency includes the time the request waits for a CPU that the client itself uses.
- Loopback only: no network latency, no TLS, no real exporter. HTTP/2 mostly pays off when a connection has a real round trip
  time or when connections are scarce; neither is the case on loopback.
- Latency is read from a histogram with four buckets per power of two and one request out of 16 is timed, so a quantile is
  accurate to about 20% (it is the upper edge of its bucket).
- Heap growth is the post-GC `HeapInuse` of the process (client and server) between the first and the last third of the window; it
  does not replace a long soak (`-duration 30m`).
- One run per cell, no confidence interval. Differences below roughly 10% are noise.

## Results

"calls/s" is HTTP requests per second, "flags/s" is flags evaluated per second (calls/s multiplied by the flags in one response:
1 for a single evaluation, 48 for the bulk endpoints). Errors were 0 in every run, and every run passed the memory-stability verdict
(heap growth between -7.5 and +1.9 MiB).

### Single flag evaluation

| Endpoint | Protocol | Workers | calls/s = flags/s | p50 | p95 | p99 |
|---|---|---:|---:|---:|---:|---:|
| `POST /v1/feature/{key}/eval` | HTTP/1.1 | 8 | 58,700 | 0.13 ms | 0.23 ms | 0.39 ms |
| | HTTP/1.1 | 64 | 60,600 | 0.92 ms | 2.6 ms | 5.2 ms |
| | h2c | 8 | 22,100 | 0.39 ms | 0.66 ms | 0.79 ms |
| | h2c | 64 | 25,400 | 2.6 ms | 3.7 ms | 4.2 ms |
| `POST /ofrep/v1/evaluate/flags/{key}` | HTTP/1.1 | 8 | 60,300 | 0.11 ms | 0.23 ms | 0.39 ms |
| | HTTP/1.1 | 64 | 62,400 | 0.92 ms | 2.6 ms | 5.2 ms |
| | h2c | 8 | 22,300 | 0.39 ms | 0.66 ms | 0.79 ms |
| | h2c | 64 | 25,400 | 2.6 ms | 3.7 ms | 4.2 ms |

### Bulk evaluation (48 flags per response)

| Endpoint | Protocol | Workers | calls/s | flags/s | p50 | p95 | p99 |
|---|---|---:|---:|---:|---:|---:|---:|
| `POST /v1/allflags` | HTTP/1.1 | 8 | 19,900 | 957,000 | 0.39 ms | 0.79 ms | 1.3 ms |
| | HTTP/1.1 | 64 | 19,900 | 954,000 | 3.1 ms | 8.4 ms | 12.6 ms |
| | h2c | 8 | 12,500 | 602,000 | 0.66 ms | 1.0 ms | 1.6 ms |
| | h2c | 64 | 14,000 | 673,000 | 5.2 ms | 7.3 ms | 8.4 ms |
| `POST /ofrep/v1/evaluate/flags` | HTTP/1.1 | 8 | 20,700 | 991,000 | 0.33 ms | 0.79 ms | 1.3 ms |
| | HTTP/1.1 | 64 | 20,500 | 982,000 | 2.6 ms | 8.4 ms | 12.6 ms |
| | h2c | 8 | 12,700 | 609,000 | 0.66 ms | 1.3 ms | 1.6 ms |
| | h2c | 64 | 14,400 | 689,000 | 4.2 ms | 7.3 ms | 8.4 ms |

## Interpretation

- **Bulk wins on evaluations per second, not on requests per second.** One bulk request costs about three single requests (20,000
  against 60,000 calls/s) but returns 48 evaluations, so the proxy evaluates about 16 times more flags per second through the bulk
  endpoints (about 0.95 million flags/s against 60,000). If a client needs most of the flags of a context (a page load, a mobile
  app start, a cache refresh), one bulk request is much cheaper than one request per flag. If it needs one or two flags, use
  the single flag endpoint: it answers in about 0.1 ms at the median against about 0.35 ms for a bulk response, and a single
  evaluation is the only path that records an event for the exporter.
- **Concurrency past the number of CPUs adds latency, not throughput.** Going from 8 to 64 workers raised throughput by 4% or
  less on HTTP/1.1 and multiplied the median latency by 7 to 8: the server was saturated at 8 workers. Size the clients of a
  single relay proxy by latency, not by the throughput you hope to gain from more connections.
- **HTTP/2 cleartext did not help in this setup, it cost 30 to 60% of the throughput (about 60% for single evaluations, 30 to 37% for bulk).** On loopback with the client and the server
  in the same process there is no round trip to hide, and HTTP/2 framing, flow control and stream handling most likely cost more CPU per
  request than HTTP/1.1 keep-alive (we did not profile it; the h2c tail latency at 64 workers was lower, because the requests are multiplexed on few
  connections and queue less on the server). Do not enable `server.enableH2C` to make the proxy faster. It is useful when a client or a
  gRPC-style proxy already speaks h2c and you want fewer connections, or to keep one protocol end to end behind a load balancer
  that terminates TLS. A measurement across a real network (client on another host) is the one that could change this conclusion.
- **Memory is flat.** No run showed heap growth beyond a few MiB over the window and the goroutine count did not move, with the
  exporter discarding events.

HTTP/2 is off by default and the option only accepts the cleartext protocol. TLS (`h2`) is not provided by the relay proxy itself:
terminate TLS in front of it.
