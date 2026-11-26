# Flag sets and evaluation

How a request finds its flag set and how its flags are evaluated in config v2: flag sets come from the control-plane manifest, API keys come with the flag set, and one process-wide evaluator answers for all of them.

> **Status:** this is built beside the running relay (`cmd/relayproxy/flagruntime` and `cmd/relayproxy/controlsource`). The relay keeps running on the version 1 configuration until the switch to config v2. Everything below that the relay does only after the switch is written in the future tense.

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

After a restart, the manifest reader can restore its saved state. The objects it carries come back as `stale` with their last good bytes, and the runtime serves them as the last good content, so a restart does not lose it.

## Evaluation is the version 1 evaluation

For the same flag files and contexts, the evaluator answers what the version 1 client answers, field by field: value, variation, reason, error code and details, metadata (including `evaluatedRuleName`), cacheable, version, `trackEvents` and failed. The same holds for the all-flags state, the listed flags, the flag configuration view and the hashes of `/v1/flag/change`. A test runs the same files through both.

- There is no client, cache, notifier or goroutine per flag set. One `Evaluator` owns one flag service for the process. It is the service the reload engine validates the flags with, so a flag is never parsed twice.
- The evaluation context enrichment (and the environment under `env`) is applied as in version 1. It can be set for all flag sets, and replaced for a named flag set.
- A flag set is selected by the route, so the evaluator methods take the flag set: `RawVariation`, the typed `...VariationDetails`, `AllFlagsState`, `GetFlagStates`. The flag set has `Configuration` and `Change`.
- `ResponseMetadata` adds `gofeatureflag_cacheable` (and `semaphore_cacheable`, unless `disableSemaphoreMetadata` is set) to the metadata of a cacheable result, as the OFREP handler does today.

## Who owns a value

The flags are shared between flag sets that list the same object, and between generations. So:

- an object or array value, and every map of the metadata (nested ones too), is a copy: a caller can change it without changing the flag or another request;
- the default value that the caller passed in is returned as given;
- the configuration view is made of deep copies;
- the flags, the maps of the runtime and the status are read only.

Copying costs one allocation per container. A flag with scalar values and flat metadata costs what it costs in version 1.

## Events

The evaluator calls an optional hook with the event of an evaluation that must be tracked: the flag sets `trackEvents` and the hook is set. Without the hook there is no cost. The exporter wiring plugs in later.

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
