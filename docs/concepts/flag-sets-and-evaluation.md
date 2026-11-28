# Flag sets and evaluation

How a request finds its flag set and how its flags are evaluated in config v2: flag sets come from the control-plane manifest, API keys come with the flag set, and one process-wide evaluator answers for all of them.

> **Status:** the relay binary runs on this (`cmd/relayproxy/flagruntime` and `cmd/relayproxy/controlsource`), the version 1 configuration is gone. Parts that are not done yet are marked as such.

## The request path

1. The HTTP layer reads the API key of the request.
2. It loads the current runtime once (`Holder.Load`). There is no lock and no allocation on this read, and the runtime is kept for the whole request, so a request never sees a mix of two generations.
3. `Runtime.Route(key)` resolves the key with one read of an immutable map and returns the flag set and a route.
4. The `Evaluator` evaluates against the flags of that flag set. The flags are shared by pointer and read only.
5. The result is a copy that the caller owns.

The key lookup is a plain map lookup: its time depends on the key as the time of any Go map lookup does. The keys are random identifiers, the answer for every mismatch is the same, and this is no weaker than the version 1 lookup.

## What a key resolves to

| Route | When | HTTP answer after the switch |
| --- | --- | --- |
| `RouteFlagSet` | the key belongs to a flag set that serves flags | the evaluation |
| `RoutePending` | the key belongs to a flag set that is not available (nothing loaded yet, or its entry is invalid and it never had a definition) | the empty answers below, never `503` |
| `RouteAdmin` | an admin key of the bootstrap file | the admin endpoints; no flag set |
| `RouteUnknown` | any other key, the empty key, or the key of a removed flag set | `401` |

### A flag set that is not available

A valid key whose flag set is not available follows the upstream behaviour:

| Endpoint | Answer |
| --- | --- |
| single evaluation, OFREP | `404` with `FLAG_NOT_FOUND` |
| single evaluation, `/v1/feature/{key}/eval` | `200` with the default value and `FLAG_NOT_FOUND` |
| bulk evaluation (OFREP, `/v1/allflags`) | `200` with an empty set |
| `/v1/flag/configuration` | `200` with an empty flag list |
| `/v1/flag/change` | `200` with the hash of the empty set |

The runtime gives these answers by itself: a flag set that is not available holds zero flags, so the evaluator answers "flag not found" and the listings are empty. The HTTP layer only needs the route to put `flag set is not available` in the error details.

### A removed flag set

- A flag set that the publisher removes leaves the routing table in the same generation. Its keys answer `401` from then on.
- Its keys are free at once: another flag set can hold them in the same generation.
- There is no last-known-good for a removed flag set. The status keeps the names of the last 256 withdrawn flag sets, never their keys.

### Key rotation

A flag set lists all its keys (`apiKeys`). To rotate, publish the flag set with both the old and the new key, move the clients, then publish it with the new key only. The surviving key never fails a request during the change. A key held by two flag sets is a conflict: the flag set that already held it keeps it, and the other is reported without printing the key.

## Last-known-good as clients see it

| Level | What a client sees |
| --- | --- |
| A flag fails validation | the previous definition of that flag; the other flags update |
| An object is stale or cannot be decoded | the flag sets that list it keep serving its last good content (`degraded`); other flag sets are untouched |
| A flag-set entry is invalid | the flag set keeps its flags and its keys (`degraded`) |
| An object is missing and was never loaded | its flags are absent; the flag set serves the rest, or is `pending` when nothing is loaded |
| The store is unreachable | the runtime keeps serving what it has; the status shows the poll error |
| A flag set is removed | its keys answer `401` |

The last good definition of a single flag is kept in the memory of the running runtime, not on disk. After a restart, a flag whose definition fails validation has no earlier definition: it is absent and counted as rejected until the object is fixed, while the other flags of the object are served.

After a restart, the manifest reader can restore its saved state. The objects it carries come back as `stale` with their last good bytes, and the runtime serves them as the last good content, so a restart does not lose it.

## Evaluation is the version 1 evaluation

For the same flag files and contexts, the evaluator answers what the version 1 client answers, field by field: value, variation, reason, error code and details, metadata (including `evaluatedRuleName`), cacheable, version, `trackEvents` and failed. The same holds for the all-flags state (compared state by state, `failed` included), the listed flags, the flag configuration view and the hashes of `/v1/flag/change`.

- The test runs the same files through both. It checks the wrapper around the engine: the enrichment and the environment, the default values, the error text, the events, the flag-not-found answer. The rule engine itself is the same code in both and is tested in its own packages.
- There is no client, cache, notifier or goroutine per flag set. One `Evaluator` owns one flag service for the process. It is the service the reload engine validates the flags with, so a flag is never parsed twice.
- The evaluation context enrichment (and the environment under `env`) is applied as in version 1. It can be set for all flag sets, and replaced for a named flag set. The evaluator keeps its own deep copy of what it is given.
- A flag set is selected by the route, so the evaluator methods take the flag set: `RawVariation`, the typed `...VariationDetails`, `AllFlagsState`, `GetFlagStates`. The flag set has `Configuration` and `Change`.
- `ResponseMetadata` adds `gofeatureflag_cacheable` (and `semaphore_cacheable`, unless `disableSemaphoreMetadata` is set) to the metadata of a cacheable result, as the OFREP handler does today.

### The flag configuration and the change hashes

- `FlagSet.Configuration(keys)` lists the flags of the flag set. The map is new for each call, the flags in it are the flag set's own (read only, see below).
- `FlagSet.Change()` answers `/v1/flag/change`. The hashes are those of the JSON encoding of the flags, byte for byte as in version 1. The hash of a flag that uses segments also covers the definitions and the members of those segments (so a publish that only changes the members of a segment changes the hash of the flags that use it, and so does the hash of the whole flag set); the hashes of the other flags are unchanged. A flag set is immutable, so the answer is computed once, by the first call (each flag is encoded once), and kept with the flag set: later calls return the same value without allocating. A flag set that a later generation carries over keeps it. The map of the answer is shared and read only.
- `FlagSet.ModifiedAt` is the time of the build that created the flag set. A flag set carried over by pointer keeps its time, a rebuilt one gets the time of its rebuild, a pending flag set has the time of its build. After the switch it will be the `Last-Modified` of the configuration endpoint.

## Who owns a value

The flags are shared between flag sets that list the same object, and between generations. The evaluator follows the read-only contract of the version 1 client: it does not copy on the hot path.

| What | Owner |
| --- | --- |
| The value of `RawVariation`, the values of the all-flags state, the value of a tracked event | the flag: shared, **read only** |
| Nested objects and arrays in the metadata of a result or a state | the flag: shared, **read only** |
| The metadata map itself, the result structs, the state table | the caller: new for every evaluation |
| The value of `JSONVariationDetails` and `JSONArrayVariationDetails` | the caller: a copy of a flag value (one copy; none when the engine already made one for a scheduled flag) |
| The default value that the caller passed in | the caller: returned as given, tracked or not |
| The map of `Configuration`, the answer of `Change` | the map of `Configuration` is new; the flags in it and the map of `Change` are shared, read only |
| The enrichment | the evaluator: a deep copy of the input; `EvaluationContextEnrichment` returns a deep copy |

- **Read only means: encode it, do not change it.** A handler that wants to change a value takes a copy with `flagdef.CloneValue` (values) or `flagdef.CloneMetadata` (metadata maps). The metadata keys that a handler adds to the metadata map of a result (the cacheable keys) are safe: the map is the caller's.
- A write into a shared part would change what every other request, and the next generation, sees. The switch to config v2 will add a guard against it in the handlers, and until then the tests check that the evaluator never writes into the flags and that readers racing with reloads only read.
- **One rule for the event, for every entry point** (`RawVariation` and the typed calls): a tracked event never holds a value that the caller owns. A value of the flag is shared and read only, so the event keeps it. A value that the caller owns gets a snapshot for the event: its own default (a flag that is not there, a disabled flag, an error of the evaluation, a value of another type) and, for a typed object or array call, the copy that the engine made for a scheduled flag. The result keeps the value as it is, so the default comes back as given. The snapshot copies `map[string]any`, `map[any]any` and `[]any` at every depth and keeps a nil one nil and of the same type (an exporter sees the same value as the caller's, and a null stays null); any other value (scalars, strings, a `[]string`, a struct) is returned as it is, by reference. The all-flags state does not track events. The exporter metadata map of the event is the one of the request context, as in version 1. The exporters must encode or copy what they keep.

Measured cost (allocations per call, the engine alone against the evaluator): the evaluator adds none. For example a scalar flag costs 1 allocation (no metadata) or 6 (a rule, flat metadata), and so does the engine alone; the all-flags state costs what the engine alone costs for the same flags. A typed object or array call adds the one copy of the value (5 allocations for a small object); a scheduled flag adds none, as the engine copies its value already.

With a hook that takes the events, a tracked evaluation adds the event (1 allocation), and a snapshot (4 for a small object) when the caller keeps the value it gets back. Measured: a scalar flag 1 without the hook, 2 with it; an object value of the raw call 6 and 7; a typed object 11 and 12; a typed scheduled object 5 and 10; a missing flag with an object default, typed, 8 and 13.

Two tests guard the figures. One compares each shape with the engine alone and takes the least cost over many runs, because the rule engine's pool makes single runs move under the race detector. The other checks that the code of the evaluator itself (the lookups, the decision to copy, the choice of enrichment, the event step without a hook, the response metadata) allocates exactly nothing.

## Events

The evaluator calls an optional hook with the event of an evaluation that must be tracked: the flag sets `trackEvents` and the hook is set. Without the hook there is no cost. The event is read only for the hook and for the exporters it feeds (see above). The exporter wiring plugs in later.

## From the manifest to the runtime

`controlsource` is the one goroutine that reads the snapshots of the manifest reader and hands each to the reload engine:

- a snapshot becomes a content: the states `ok`, `stale` and `missing` map one to one, with their reason and checksum, and the bytes are shared, not copied;
- the reader delivers the newest snapshot only (latest wins), and so does the engine: a burst of generations is built once, for the last one;
- the goroutine ends with its context, or when the reader closes its channel;
- `Source.Status()` joins the status of the reader, the result of the last round of the engine and the status tree of the running runtime, without importing HTTP.

See [live reload](live-reload.md) for the engine and [control-plane polling](control-plane-polling.md) for the reader.

## What changes at the cut-over

- Flag sets and their API keys come from the published flag-set documents, not from the relay configuration file; the relay starts with no flag set (every key answers `401`).
- The handlers call the evaluator on the flag set of the route, instead of a client per flag set.
- The exporter hook, the readiness view and the observability endpoints use `Source.Status()`.
- Admin keys stay in the bootstrap file.
- The handlers will get a guard that fails a test when a handler writes into a result of the evaluator (the read-only contract above).
- The `Last-Modified` header of the configuration endpoint will come from `FlagSet.ModifiedAt`.
