# API endpoints

## Overview
The relay-proxy is providing a list of REST endpoints to interact with Semaphore.

Since we are evolving the relay proxy, the list of endpoints can change, and we recommend checking the [**OpenAPI documentation**](https://github.com/BlackbirdWorks/semaphore/blob/main/cmd/relayproxy/api/testdata/openapi.golden.json) to have the most updated list of endpoints.

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
| `POST /admin/v1/retriever/refresh`      | Force a refresh of the retrievers.                                     |
| `GET /health`, `GET /info`, `GET /metrics` | Monitoring (on `monitoringPort` when it is set; `/metrics` needs metrics enabled). |
| `GET /swagger/*`                        | Swagger UI, only with `enableSwagger: true`.                           |

The streams are listed in [Flag change streams](#flag-change-streams).

### Swagger
The relay proxy can expose a [swagger UI](https://swagger.io/tools/swagger-ui/) to test your REST endpoints.

This swagger UI is available at the `/swagger/index.html` endpoint (the document it displays is `/swagger/openapi.json`) and will show all the available endpoints and the parameters needed. The page loads the Swagger UI scripts from a public CDN, so the browser needs internet access.

> [!NOTE]
> By default, this endpoint is not exposed, you need to have this configuration in your **relay proxy** configuration file:
>
> ```yaml
> # ...
> enableSwagger: true
> host: my-proxy-domain.com # the DNS to access the proxy
> ```
>
> When enabled, you can go to the `/swagger/` endpoint with your browser, and you will have access to the Swagger UI for the relay proxy. 

## OpenAPI documentation

The OpenAPI 3.1 document is generated from the typed operations the relay proxy registers. With `enableSwagger: true` it is served at `/swagger/openapi.json` (and `/swagger/openapi.yaml`, plus the 3.0 downgrade `/swagger/openapi-3.0.json`). A test compares it with the committed copy `cmd/relayproxy/api/testdata/openapi.golden.json`,.

## Flag change streams

The relay proxy can push a message to your application each time the flag configuration changes.

| Transport | Endpoint                        | Notes                                                       |
|-----------|---------------------------------|-------------------------------------------------------------|
| WebSocket | `/stream/v1/ws/flag/change`     | Receives the diff of the changed flags.                     |
| WebSocket | `/ws/v1/flag/change`            | **Deprecated** alias of `/stream/v1/ws/flag/change`.        |
| SSE       | `/stream/v1/sse/flag/change`    | Receives a `refetchEvaluation` event (OFREP ADR-0008).      |

The API key is passed with the `apiKey` query parameter, for example `ws://localhost:1031/stream/v1/ws/flag/change?apiKey=<your-api-key>`.

### Streams with flag sets

When the relay proxy runs with [flag sets](../concepts/flagset.md), each stream is scoped to a single flag set:

- The connection only receives the changes of the flag set associated to its API key.
- The API key **must be associated to a flag set**. A missing or unknown key is rejected with `401`, and a valid key that is not associated to any flag set (for example an `authorizedKeys.admin` key) is rejected with `400`, before the WebSocket upgrade.
- To watch several flag sets, open **one connection per flag set**, each with an API key of that flag set.

> [!WARNING]
> Older versions of the relay proxy sent the changes of every flag set to every WebSocket client. If you relied on this behavior, open one connection per flag set.
> If you still use the deprecated `/ws/v1/flag/change` endpoint, it follows the same rules; migrate to `/stream/v1/ws/flag/change` when you update your clients.

Without flag sets, nothing changes: every connection receives all flag changes.
