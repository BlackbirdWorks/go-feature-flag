# loadtest

Tools to measure the throughput and the memory stability of the evaluation hot path.

## Benchmarks

| What | Where | Run |
| --- | --- | --- |
| Library variations per scenario (no rule, rule, multiple rules, JsonLogic, percentage, progressive, scheduled, large `in` list), tracking off/on, serial/parallel, all-flags state (10/100/1000) | `variation_scenarios_bench_test.go` (build tag `bench`) | `go test -tags bench -run xxx -bench 'Scenarios\|AllFlagsState' -benchmem .` |
| Relay proxy handlers in-process (GOFF eval/allflags, OFREP eval/bulk) | `internal/loadtest/relay/handlers_bench_test.go` | `go test -run xxx -bench Handlers -benchmem ./internal/loadtest/relay` |
| Event store, flush cycle, manager with discard/slow/failing exporter | `exporter/exporter_bench_test.go` | `go test -run xxx -bench . -benchmem ./exporter` |
| Flag cache reads, with concurrent `UpdateCache` | `internal/cache/cache_bench_test.go` | `go test -run xxx -bench . -benchmem ./internal/cache` |

Compare two runs with `benchstat old.txt new.txt` (use `-count=10`).

## Soak

`internal/loadtest/cmd/soak` evaluates at max rate with N goroutines against the embedded library
or a real relay proxy over loopback HTTP, with event tracking on and a selectable exporter
behaviour (`discard`, `slow` = 2s per batch, `failing`), and prints one line per interval:
evals/s, p50/p99 latency (1 evaluation out of 16 is timed), post-GC heap (HeapAlloc, HeapInuse,
HeapObjects), NumGC and goroutines.

```
go run ./internal/loadtest/cmd/soak -target library -exporter slow -duration 60s
go run ./internal/loadtest/cmd/soak -target relay -endpoint goff-eval -exporter failing -duration 60s
go run ./internal/loadtest/cmd/soak -target relay -endpoint ofrep-bulk -flag x -duration 60s
```

Endpoints: `goff-eval`, `goff-allflags`, `ofrep-eval`, `ofrep-bulk`. `-cpuprofile` writes a CPU profile.
For the relay target the proxy runs in the same process as the load generator (the heap and the
CPU are shared), the exporter is the webhook exporter pointed at a local sink with the chosen
behaviour.

The final line is the verdict ("memory stable", exit status 1 on FAIL):

- after a warm-up (`-warmup`, default max(2, 20%) of the samples) the mean post-GC HeapInuse of the
  last third of the samples must not exceed the first third by more than
  max(`-heap-tolerance` (25%), 32 MiB);
- the goroutine count must not exceed its value at the end of the warm-up by more than 64;
- the run is aborted and fails when the live heap exceeds 6 GiB.

The run needs at least 3 samples after the warm-up: use `-duration` >= 6 x `-interval`.
