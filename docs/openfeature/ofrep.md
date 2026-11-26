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
- If [API keys](../relay-proxy/configuration.md) are configured, pass the key via `X-API-Key` or `Authorization: Bearer <key>`.
- The relay proxy does not implement `GET /ofrep/v1/configuration`.

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

## Evaluation context

The OFREP context maps directly to the OpenFeature evaluation context. See [evaluation context](../concepts/evaluation-context.md) for reserved keys understood by the relay proxy (such as `semaphore`, and its legacy name `gofeatureflag`).

## Not available through OFREP

- **In-process evaluation**: Language-specific in-process providers are not supported; only the Go module evaluates flags in-process.
- **Flag usage events**: The relay proxy generates flag-usage events at evaluation time; see [flag usage tracking](../tracking/flag-usage-tracking.md).
