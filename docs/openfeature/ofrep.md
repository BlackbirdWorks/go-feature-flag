# OpenFeature Remote Evaluation Protocol (OFREP)

## Overview

Applications integrate with Semaphore through standard [OpenFeature](https://openfeature.dev) SDKs and generic OpenFeature Remote Evaluation Protocol (OFREP) providers. This repository does not ship language-specific providers.

## What is OFREP?

OFREP is a vendor-neutral API specification that lets OpenFeature SDKs evaluate flags against any compatible backend without custom providers or vendor lock-in. See the [OpenFeature protocol repository](https://github.com/open-feature/protocol/) for the full specification.

## Use OFREP with Semaphore

Run the [relay proxy](../relay-proxy/README.md) in your infrastructure. It exposes these OFREP endpoints:

| Endpoint | Usage |
|---|---|
| `POST /ofrep/v1/evaluate/flags/{key}` | Evaluate a single flag (server-side SDKs). |
| `POST /ofrep/v1/evaluate/flags` | Bulk-evaluate all flags (client-side SDKs). |

- See the [endpoints reference](../relay-proxy/endpoints.md) and `cmd/relayproxy/api/testdata/openapi.golden.json` for full request and response schemas.
- Point your language's OFREP provider at the relay proxy base URL (for example, `http://localhost:1031`).
- If [API keys](../relay-proxy/bootstrap-config.md) are configured, pass the key via `X-API-Key` or `Authorization: Bearer <key>`.
- The relay proxy does not implement `GET /ofrep/v1/configuration`: the pinned specification (below) does not define it.
- Request bodies are limited to 1 MiB (`400` with `INVALID_CONTEXT`).
- `If-None-Match` on the bulk endpoint takes one tag, a comma separated list of tags (weak or strong), or `*`; a match gives `304` without a body.
- An admin key is not linked to a flag set: it gets `403` on the OFREP endpoints.
- Set `server.ofrepEventStream` to advertise the flag change stream in the `eventStreams` field of the bulk response.

### JavaScript SDK examples

Server side (Node.js, `npm install @openfeature/server-sdk @openfeature/ofrep-provider`):

```javascript
import { OpenFeature } from '@openfeature/server-sdk';
import { OFREPProvider } from '@openfeature/ofrep-provider';

await OpenFeature.setProviderAndWait(
  new OFREPProvider({
    baseUrl: 'http://localhost:1031',
    headers: [['X-API-Key', 'my-api-key']], // only if API keys are configured
  })
);
const client = OpenFeature.getClient();
const enabled = await client.getBooleanValue('my-flag', false, { targetingKey: 'user-123' });
```

Browser (`npm install @openfeature/web-sdk @openfeature/ofrep-web-provider`) uses the bulk endpoint and `OpenFeature.setContext(...)`:

```javascript
import { OpenFeature } from '@openfeature/web-sdk';
import { OFREPWebProvider } from '@openfeature/ofrep-web-provider';

await OpenFeature.setContext({ targetingKey: 'user-123' });
await OpenFeature.setProviderAndWait(new OFREPWebProvider({ baseUrl: 'http://localhost:1031' }));
const enabled = OpenFeature.getClient().getBooleanValue('my-flag', false);
```

For other languages, use the official OFREP provider in the [OpenFeature ecosystem](https://openfeature.dev/ecosystem).

### curl example

```shell
curl 'http://localhost:1031/ofrep/v1/evaluate/flags/my-flag' -X POST \
  -H 'Content-Type: application/json' \
  --data-raw '{"context": {"targetingKey": "user-123"}}'
```

## Specification version and what is verified

The relay proxy follows the OFREP OpenAPI document version `0.4.0`, vendored byte for byte in `openfeature/provider_tests/ofrep-conformance/testdata/openapi.yaml`. `openapi.pin.json` next to it records the source (`open-feature/protocol`, `service/openapi.yaml`), the commit and the SHA-256; a test fails if the file differs from it. The protocol repository publishes no release tag, so the pin is a commit.

The conformance suite (`task ofrep-conformance`, also part of `go test ./...`) starts real relays and checks every response against that document:

- Both operations and every status code the relay sends (`200`, `304`, `400`, `401`, `403`, `404`), with the response schema, the `ETag` header, the `Content-Type` and the error bodies.
- All value types (boolean, string, integer, float, object, and no `value` for a disabled flag), the `reason` and `errorCode` enumerations, and `metadata` value types.
- Request handling: missing, null or malformed `context`, wrong content type, empty and oversized bodies, unknown fields, a flag key that needs URL escaping, other HTTP methods.
- Authentication: `X-API-Key` and `Authorization: Bearer`, with and without keys (public flag sets), and an admin key.
- `If-None-Match` (strong, weak, list, `*`) and the `flagConfigEtag` and `flagConfigLastModified` query parameters.
- The `eventStreams` field of the bulk response.
- The OpenAPI document that the relay publishes about itself agrees with the pinned one for these paths.

A coverage test lists every operation, status code, header, schema, parameter, security scheme and value type of the pinned document and fails when one has no case, so a new version of the document cannot be adopted with silent gaps. Two responses cannot be provoked: the relay has no rate limit, so it never answers `429` (and sends no `Retry-After`), and it answers `500` only for a fault of its own.

## Evaluation context

The OFREP context maps directly to the OpenFeature evaluation context. See [evaluation context](../concepts/evaluation-context.md) for reserved keys understood by the relay proxy (such as `semaphore`, and its legacy name `gofeatureflag`).

## Not available through OFREP

- **In-process evaluation**: Language-specific in-process providers are not supported; only the Go module evaluates flags in-process.
- **Flag usage events**: The relay proxy generates flag-usage events at evaluation time; see [flag usage tracking](../tracking/flag-usage-tracking.md).
