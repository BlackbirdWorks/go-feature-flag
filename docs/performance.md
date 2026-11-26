# Performance

Measured throughput and latency of the relay proxy for single flag evaluations and bulk evaluations, over HTTP/1.1
and HTTP/2 cleartext (h2c). The numbers come from the soak harness in `internal/loadtest`, so you can repeat them.
Treat them as a comparison between the modes on one machine, not as a capacity promise: the client and the server
run in the same process on the same CPUs.

## Setup

| | |
|---|---|
| Hardware | Commodity 4-core / 8-thread x86_64 Linux machine (`GOMAXPROCS=8`) |
| OS, Go | Linux, Go 1.27 |
| Load average before the run | 0.92 (1-minute); ~4 to 6 during runs |
| Server | Relay proxy in-process (`internal/loadtest/relay`), file retriever, 48 flags, `exporter: discard` (webhook sink returning `200`) |
| Client | `net/http` client in the same process, `N` closed-loop worker goroutines, 10 attributes in the evaluation context, 1,000 distinct users |
| Protocols | HTTP/1.1 with keep-alive, or h2c with prior knowledge (`server.enableH2C: true`; no HTTP/1.1 fallback, every response protocol is verified) |
| Duration | 40 s per run; first 10 s (two samples) are warm-up, table shows the mean of the remaining 25 to 30 s |
| Tracking | Enabled for single-flag endpoints (`rule-bool-track`: every evaluation queues an event discarded by the exporter); bulk endpoints do not queue events |

Reproduce one cell:

```shell
go run ./internal/loadtest/cmd/soak -target relay -endpoint ofrep-bulk -protocol h2c \
  -workers 64 -exporter discard -duration 40s -interval 5s -warmup 2
```

**Limitations** (read before quoting numbers):

- Client and server share CPUs, Go scheduler, and garbage collector. Absolute numbers would be higher with the load generator on a separate host, and measured latency includes time waiting for CPU used by the client.
- Loopback only: no network latency, TLS, or real exporter. HTTP/2 mainly helps when connections have real round-trip latency or are scarce; neither applies on loopback.
- Latency comes from a histogram with four buckets per power of two, sampling 1 in 16 requests, so quantiles are accurate to ~20% (upper edge of their bucket).
- Heap growth is post-GC `HeapInuse` (client and server) between the first and last third of the window; it does not replace a longer soak (`-duration 30m`).
- One run per cell, no confidence intervals. Differences below ~10% are noise.

## Results

`calls/s` is HTTP requests per second; `flags/s` is flags evaluated per second (`calls/s` × flags per response: 1 for single evaluation, 48 for bulk). Every run had 0 errors and passed the memory-stability check (heap growth between `-7.5` and `+1.9` MiB).

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

- **Bulk wins on evaluations per second, not requests per second.** One bulk request costs ~3× a single request (20,000 vs. 60,000 calls/s) but returns 48 evaluations, yielding ~16× more flags/s (~0.95 million vs. 60,000). Use bulk when a client needs most flags for a context (page load, app startup, cache refresh). Use the single-flag endpoint when you need one or two flags: median latency is ~0.1 ms vs. ~0.35 ms for bulk, and only single evaluations record exporter events.
- **Concurrency past the CPU count adds latency, not throughput.** Increasing workers from 8 to 64 raised HTTP/1.1 throughput by ≤4% while multiplying median latency by 7–8× (the server saturated at 8 workers). Size clients by latency, not by hoping more connections increase throughput.
- **h2c reduced throughput by 30 to 60% in this setup** (~60% for single evaluations, 30 to 37% for bulk). On loopback in one process there is no round trip to hide, and HTTP/2 framing, flow control, and stream handling likely cost more CPU per request than HTTP/1.1 keep-alive (not profiled; h2c tail latency at 64 workers was lower because multiplexed streams queued less on the server). Do not enable `server.enableH2C` for speed—use it when clients or proxies already speak h2c and you want fewer connections or end-to-end HTTP/2 behind TLS termination. Results over a real network could differ.
- **Memory stays flat.** Heap growth remained within a few MiB and goroutine counts did not grow (with the exporter discarding events).

HTTP/2 is off by default and only supports cleartext (`h2c`). The relay proxy does not serve TLS (`h2`); terminate TLS in front of it.
