# Live reload

The relay changes what it serves while it runs: no restart, no redeploy, no failed in-flight request. This page describes the reload engine in `cmd/relayproxy/flagruntime`.

> **Status:** the engine is built beside the running relay. It becomes active when the relay switches to config v2 (a later release). Until then the relay keeps reloading as described in the [relay configuration](../relay-proxy/configuration.md).

## How a reload works

- One goroutine, the reconcile loop, is the only writer. Work reaches it through Go channels:
  - a size-1 channel of published content (a newer snapshot replaces one that was not taken yet: latest wins);
  - a size-1 channel of bootstrap configurations (same rule);
  - the stop signal of the context.
- A content or a bootstrap configuration belongs to the engine from the moment it is submitted: the caller must not change it afterwards. The engine does not copy it.
- A burst of inputs is coalesced into one round. Each round:
  1. **Validate** the content (generation, object states, formats).
  2. **Plan**: diff by object name and checksum, and by flag-set definition.
  3. **Build** a candidate off to the side. The running runtime is not touched.
  4. **Swap**: one atomic pointer store. A reader sees the old runtime or the new one, never a mix.
  5. **Release**: emit change events and drop the parse results nothing refers to any more.
- Readers load the runtime once per request (`Holder.Load`) and keep it for the whole request. There is no lock on the read path.
- A candidate that fails validation or build stores nothing.

### Generations

- The generation only advances. A lower generation is ignored.
- At the running generation, a content is a **conflict** (rejected, `generation-conflict`) only when it has another set of object names, another kind for a name, or another checksum for a name where both the running content and the new one carry content. A change of state at the same generation is accepted and built: an object that was `pending`, `missing` or `stale` becomes `ok` (and the reverse) without waiting for a new generation. The same objects in the same states are ignored (`duplicate-generation`).

### Where the result of a round is read

A rejected round stores nothing, so the running runtime does not show it. The last round result (applied, ignored or rejected, with its reason) is read from `Engine.Result()`; the relay wiring exposes it. The next runtime that is stored also carries it as `Status.LastRejected`.

## Last-known-good, level by level

A failure keeps the last good version of its own level. Nothing above it is frozen.

| Level | Failure | What is kept |
| --- | --- | --- |
| Flag | a flag fails validation | the previous definition of that flag (counted in the status) |
| Source object | stale, missing, or not decodable | the previous parsed table of the object |
| Flag-set entry | invalid entry, unresolved source | the flags the flag set had, with its keys |
| API key | held by two flag sets | the flag set that already held it; the other is reported (the key is never printed) |

A later source wins on a duplicate flag key.

### An invalid flag-set entry or an unresolved source

The rule is the same on first load and later:

- the flag set had a definition: it keeps the flags it had (`degraded`, problem `definition-kept`). It does not follow its sources until its entry is valid again, so a partial list of sources is never served;
- it had none, and its name and keys are valid: it gets a placeholder that routes its keys and serves no flag (`pending`), so a valid key reaches the "not available" answer;
- its name or a key is invalid: it is reported in the status only.

### Flags object forms

A flags object declares its format (`yaml`, `json` or `toml`) and is read by the flag parser of the relay engine, exactly as the version 1 relay reads a flag file: the runtime does no decoding of its own. A form that the parser rejects is a decode error: the object keeps its last good table (`invalid`).

| Form | json | yaml | toml |
| --- | --- | --- | --- |
| blank or whitespace only | error | no flag | no flag |
| comment only | error | no flag | no flag |
| `null` | no flag | no flag | error |
| `{}` | no flag | no flag | error |
| `[]` | error | error | error |

### Known limits

These are the behaviours of the engine parser, kept for parity:

- JSON integers above 2^53 are read as floats and rounded (`9007199254740993` is served as `9007199254740992`). YAML keeps such an integer exact: publish those flags as YAML.
- In a JSON flag, a mix of small and large numbers is read as floats, so it validates.
- A JSON escape of a surrogate pair is read as the character; a lone surrogate escape is read as the replacement character (U+FFFD) without an error.
- A repeated member name in a JSON object is accepted and the later one wins. Rejecting repeated names is the job of the publisher side: `controlplane.UnmarshalFlags` rejects them, so check a flags file there before it is published.

### Flag-set states

- `serving`: every source is current and every flag is valid.
- `degraded`: it serves, but a source is stale or missing, a flag was rejected, or an entry problem was reported.
- `pending`: no source was ever loaded, or the entry is invalid and never had a definition. It serves zero flags and everything else keeps loading.
- `withdrawn`: the publisher removed it (see below).

An empty runtime (no flag sets, no flags) is valid and is served.

### Withdrawn flag sets

A flag set that the publisher removes (from its document, or by removing the document) leaves the flag-set table and the key table in that generation. Its keys are unknown from then on (the relay answers as for any unknown key) and are free for another flag set at once, in the same generation or later. The status lists the most recently withdrawn names with the generation that removed them (never keys). The list is bounded (default 256, the oldest are dropped). The engine keeps no other state for a withdrawn flag set.

## What needs a restart

Admin keys apply live, through the same swap. Settings that cannot change while running are reported in `pendingRestart` and not applied:

- listeners and the unix socket (`server.listeners`, `server.unixSocketPath`);
- the connection and state directory of the control plane, and its manifest settings;
- `connections`, `exporters`, `notifiers`, `observability`, and the bootstrap `version`.

Only the setting names are listed, never their values. The first accepted configuration is the baseline.

## Scale

The engine targets thousands of flag sets with dozens to a hundred flags each. The cost of a reload follows what changed:

- a flags object is parsed once per checksum, kept in a cache that survives generations, and dropped when no object refers to it;
- a flag set whose definition and sources are unchanged is carried over by pointer;
- a flag that is unchanged in a changed object keeps its pointer, so flags exist once across versions;
- the flags of a source are shared by every flag set that lists it (a flag set with one source shares the map itself);
- a generation that changed only flags objects rebuilds only the flag sets that list them. A no-op generation parses and rebuilds nothing.

Costs that remain proportional to the fleet, and nothing else:

- every round scans the objects of the content (O(objects), checksum comparison, no parsing of unchanged bytes);
- a round that changes a flag set copies the flag-set table, the status table and the key table once (O(flag sets) and O(keys) map copies, no per-flag work);
- a round that changes a flag-set document, or adds or removes any object, also walks every entry of the documents (O(entries + keys)) and reuses the unchanged flag sets by pointer. Documents that did not change are not validated again unless a flags object name was added or removed; each document that is validated costs O(objects) for the lookup of its sources, so that walk costs O(validated documents x objects) on top.

The builder holds the running and the candidate version of a changed flag set, and one version of an unchanged one. Older versions stay reachable only while a reader (a request that loaded its runtime) or an event nobody consumed still holds them, so the memory in use depends on how long readers keep a runtime, not on a fixed number of versions.

Values of variations are shared between flag sets: treat a returned value as read-only.

### Flag set hash

`FlagSet.Hash` identifies the evaluable content of a flag set: it changes when, and only when, a flag of the set (its key or its parsed definition) changes. It does not depend on comments, formats, checksums, the order of the sources, or history. It is built from a hash of every parsed flag, computed once at parse time. One exception: a flag that cannot be encoded canonically (for example a NaN in its metadata) is hashed from the checksum of its object and its key, so any edit of that object, even a comment, changes the hash of the flag sets that use it.

## Change events

After a swap the engine sends one event per rebuilt flag set whose flags changed (added, deleted and updated flags, found by pointer comparison). A withdrawn flag set that the consumer knows (an event for it was delivered or waits) always gets a `Removed` event, even when its flags were all deleted before; a flag set the consumer never heard of (it never had a flag) gets none, so churn of empty flag sets queues nothing. The consumer reads a bounded channel. If it is slow, events wait per flag set and merge with later changes of the same flag set, so no flag-set name is lost and the engine never blocks. A flag set that is created and removed while its event waits produces no event.

The events of one flag set arrive in order. Events of different flag sets are not ordered by generation: a waiting event keeps the place of the first change it merged.

## Secrets

API keys are plain strings in the published flag-set documents. They are never printed in logs, errors or status.
