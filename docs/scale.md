# Scale

The target: thousands of flag sets, each with dozens to a hundred flags, reloaded in place and atomically. The suite
proves it at 5000 flag sets x 100 flags (500,000 flags, 500 flags objects shared by ten flag sets each).

## What is tested

- A change of one flags object rebuilds only the flag sets that list it. Every other flag set keeps its pointer, and the
  object is parsed once per checksum.
- The cost of a one-object change follows the change, not the fleet: the same change in a 10x larger fleet allocates
  the same number of objects, and each extra unchanged flag set adds a small fraction of what a rebuilt one costs.
- A generation that changes every object replaces every flag set in one swap, and a reader never sees a mix of two
  generations.
- The engine is one goroutine, whatever the number of flag sets.
- Readers that evaluate while the engine swaps repeatedly get no failed evaluation, and every value is the one of the
  generation of the runtime the reader loaded.
- The live heap during a swap stays within two generations (a reader still holding the old runtime), and one changed
  object adds memory in proportion to the flag sets it rebuilds: see [Measured](relay-proxy/relay-core.md#measured).
- A cold start with the state directory fetches no object (one conditional poll of the manifest). Without it, the
  relay fetches every object exactly once.

The tests assert counts and pointers, never wall-clock time.

## Run it

The normal test run uses 500 flag sets x 100 flags (`go test -race ./cmd/relayproxy/flagruntime ./cmd/relayproxy/controlsource`).
The full size runs with the `bench` build tag:

```shell
go test -race -tags=bench -run TestScale ./cmd/relayproxy/flagruntime ./cmd/relayproxy/controlsource
go test -tags=bench -run '^$' -bench 'BenchmarkScale|BenchmarkBuild' -benchmem ./cmd/relayproxy/flagruntime
```

## Numbers

Measured at 5000 flag sets x 100 flags on a commodity 4-core Linux machine, without `-race`:

| | Time | Memory | Allocations |
|---|---|---|---|
| Build the whole fleet (cold start) | 0.47 s | 197 MB | 3.7 M |
| Swap a generation that changes every flag set | 0.32 s | 175 MB, heap peak about 160 MB | 1.8 M |
| Change one flags object (ten flag sets) | 3.2 ms | 1.3 MB | 7,000 |

`BenchmarkScaleReadersDuringSwaps` reports the p99 latency of an evaluation while whole generations swap
(`eval-p99-ns`); it is a figure to read, not a threshold.
