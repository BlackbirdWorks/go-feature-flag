# API endpoints

## Overview

The relay proxy exposes REST and streaming endpoints for flag evaluation, tracking, configuration, and monitoring.
Check the [**OpenAPI specification**](https://github.com/BlackbirdWorks/semaphore/blob/main/cmd/relayproxy/api/testdata/openapi.golden.json) for the full, up-to-date schema.

### Endpoint list

| Method and path                         | Usage                                                                  |
|-----------------------------------------|------------------------------------------------------------------------|
| `POST /ofrep/v1/evaluate/flags/{key}`   | OFREP evaluation of a single flag.                                     |
| `POST /ofrep/v1/evaluate/flags`         | OFREP bulk evaluation of all flags.                                    |
| `POST /v1/feature/{key}/eval`           | Relay proxy API: evaluate a single flag.                               |
| `POST /v1/allflags`                     | Relay proxy API: evaluate all flags.                                   |
| `POST /v1/data/collector`               | Send evaluation events to the configured exporters.                    |
| `GET /v1/flag/change`                   | Hash of the current flag configuration (cheap change detection).       |
| `POST /v1/flag/configuration`           | Current flag configuration.                                            |
| `GET /openfeature/v0/manifest`          | OpenFeature flag manifest.                                             |
| `POST /admin/v1/flags/validate`         | Validate a flag file.                                                  |
| `POST /admin/v1/retriever/refresh`      | Poll the control plane now (admin key); answers the result, the generation and whether it changed. |
| `GET /health`, `GET /info`, `GET /metrics` | Monitoring (on `monitoringPort` when set; `/metrics` requires metrics enabled). |

Streaming endpoints are listed in [Flag change streams](#flag-change-streams).

### Flags that use segments

- `POST /v1/flag/configuration` lists, for each flag whose rules use [segments](../configure-flags/segments.md), the sorted `segments` it uses and `requiresServerEvaluation`: `true` when one of them is [dynamic](../configure-flags/segments-dynamic.md#cacheable-and-flag-configuration) (its members live in a store) or cannot be resolved by the flag set: only the relay can answer. A flag that uses no segment has neither field, and no member of any segment is ever listed.
- An evaluation of a flag that uses a dynamic segment is never cacheable: the OFREP single evaluation carries neither `semaphore_cacheable` nor `gofeatureflag_cacheable`, and `POST /v1/feature/{key}/eval` answers `"cacheable": false`. The bulk endpoints (`/ofrep/v1/evaluate/flags`, `/v1/allflags`) never send the cacheable keys.

## OpenAPI documentation

The OpenAPI 3.1 document is generated from the typed operations registered by the relay proxy. The relay does not serve Swagger UI or the document at runtime. The document is committed as `cmd/relayproxy/api/testdata/openapi.golden.json` and a test compares it with the operations the relay registers. The document covers the entire API, including monitoring endpoints, and advertises no server.

## Flag change streams

The relay proxy can push notifications to your application whenever the flag configuration changes:

| Transport | Endpoint                        | Notes                                                       |
|-----------|---------------------------------|-------------------------------------------------------------|
| WebSocket | `/stream/v1/ws/flag/change`     | Receives the diff of changed flags.                         |
| WebSocket | `/ws/v1/flag/change`            | **Deprecated** alias of `/stream/v1/ws/flag/change`.        |
| SSE       | `/stream/v1/sse/flag/change`    | Receives a `refetchEvaluation` event (OFREP ADR-0008).      |

Pass the API key with the `apiKey` query parameter, for example `ws://localhost:1031/stream/v1/ws/flag/change?apiKey=<your-api-key>`.

### Streams with flag sets

When running with [flag sets](../concepts/flagset.md), each stream is scoped to a single flag set:

- The connection receives only changes for the flag set associated with its API key.
- The API key **must belong to a flag set**. A missing or unknown key returns `401`; a valid key not associated with any flag set (such as `authorizedKeys.admin`) returns `400` before the WebSocket upgrade.
- To watch multiple flag sets, open **one connection per flag set**, each using an API key for that flag set.

> [!WARNING]
### Open streams follow the keys

An open stream has the rights of a new request. After a generation that removes an API key, moves it to another flag
set or withdraws its flag set, every stream opened with that key (WebSocket and SSE) is closed at once and receives none
of the changes of that generation. The client reconnects like any new request: a key that was removed, or whose flag set
was withdrawn, is refused; a key that moved to another flag set reconnects and lands on its new flag set.

- Other keys of the same flag set, and streams without a key (see public flags), are not touched.
- A key rotation with an overlap (the old and the new key both valid in a generation) disconnects nobody; the old
  streams end in the generation that drops the old key.
- The relay keeps a keyed hash of the key a client authenticated with (never the key), and checks it when the runtime
  is swapped, not per message. The check never waits for a client: the sockets are closed by a worker of their own.
- A connection that registers with a key receives nothing until the relay checked the key again after the registration,
  so a key revoked by a swap that ran before it never gets a change.
- Every write of an SSE stream has a 10 second deadline, renewed per write: a client that stopped reading is dropped, and
  a revoked stream ends at once even if its write is blocked on the network.

> Older versions sent changes from every flag set to every WebSocket client. Open one connection per flag set if you need multiple sets.
> The deprecated `/ws/v1/flag/change` endpoint follows the same rules; migrate clients to `/stream/v1/ws/flag/change`.

Without flag sets, every connection receives all flag changes.
