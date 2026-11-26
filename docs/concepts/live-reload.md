# Live reload

The relay changes what it serves while it runs: no restart, no redeploy, no failed in-flight request. This page describes the reload engine in `cmd/relayproxy/runtime`.

> **Status:** the engine is built beside the running relay. It becomes active when the relay switches to config v2 (a later release). Until then the relay keeps reloading as described in the [relay configuration](../relay-proxy/configuration.md).

## How a reload works

- One goroutine, the reconcile loop, is the only writer. Work reaches it through Go channels:
  - a size-1 channel of published content (a newer snapshot replaces one that was not taken yet: latest wins);
  - a size-1 channel of bootstrap configurations (same rule);
  - the stop signal of the context.
- A burst of inputs is coalesced into one round. Each round:
  1. **Validate** the content (generation, object states, formats).
  2. **Plan**: diff by object name and checksum, and by flag-set definition.
  3. **Build** a candidate off to the side. The running runtime is not touched.
  4. **Swap**: one atomic pointer store. A reader sees the old runtime or the new one, never a mix.
  5. **Release**: emit change events and drop the parse results nothing refers to any more.
- Readers load the runtime once per request (`Holder.Load`) and keep it for the whole request. There is no lock on the read path.
- A candidate that fails validation or build stores nothing. The reason is recorded in the next status and in the last reload result.
- The generation only advances. A lower generation is ignored. The same generation with other content is rejected (`generation-conflict`).

## Last-known-good, level by level

A failure keeps the last good version of its own level. Nothing above it is frozen.

| Level | Failure | What is kept |
| --- | --- | --- |
| Flag | a flag fails validation | the previous definition of that flag (counted in the status) |
| Source object | stale, missing, or not decodable | the previous parsed table of the object |
| Flag-set entry | invalid entry, unresolved source | the previous definition of the flag set, with its keys |
| API key | held by two flag sets | the flag set that already held it; the other is reported (the key is never printed) |

A later source wins on a duplicate flag key.

Flag-set states:

- `serving`: every source is current and every flag is valid.
- `degraded`: it serves, but a source is stale or missing, a flag was rejected, or an entry problem was reported.
- `pending`: no source was ever loaded. It serves zero flags and everything else keeps loading.
- `withdrawn`: a later manifest removed it. Its keys still route to it so a request gets the "not available" answer.

An empty runtime (no flag sets, no flags) is valid and is served.

## What needs a restart

Admin keys apply live, through the same swap. Settings that cannot change while running are reported in `pendingRestart` and not applied:

- listeners and the unix socket (`server.listeners`, `server.unixSocketPath`);
- the connection and state directory of the control plane, and its manifest settings;
- `connections`, `exporters`, `notifiers`, `observability`, and the bootstrap `version`.

Only the setting names are listed, never their values.

## Scale

The engine targets thousands of flag sets with dozens to a hundred flags each. The cost of a reload follows what changed:

- a flags object is parsed once per checksum, kept in a cache that survives generations, and dropped when no object refers to it;
- a flag set whose definition and sources are unchanged is carried over by pointer;
- a flag that is unchanged in a changed object keeps its pointer, so flags exist once across versions;
- the flags of a source are shared by every flag set that lists it (a flag set with one source shares the map itself);
- a generation that changes only flags objects rebuilds only the flag sets that list them. A no-op generation parses and rebuilds nothing;
- the key table (`API key` to flag set) is rebuilt per generation that changes a flag set or a key: O(keys), not O(flags).

During a swap at most two versions of a changed flag set are reachable. Unchanged flag sets exist once.

Values of variations are shared between flag sets: treat a returned value as read-only.

## Change events

After a swap the engine sends one event per rebuilt flag set (added, deleted and updated flags, found by pointer comparison). The consumer reads a bounded channel. If it is slow, events wait per flag set and merge with later changes of the same flag set; the engine never blocks and no flag-set name is lost.

## Secrets

API keys are plain strings in the published flag-set documents. They are never printed in logs, errors or status.
