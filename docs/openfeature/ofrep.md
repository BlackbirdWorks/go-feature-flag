# OpenFeature Remote Evaluation Protocol (OFREP)

## Overview
Semaphore is used from applications through the standard [OpenFeature](https://openfeature.dev) SDKs
and the generic OpenFeature Remote Evaluation Protocol (OFREP) providers. This repository does not ship
language-specific providers.

## What is OFREP?
OFREP is an API specification that allows you to connect your applications to any feature flag management system that supports the protocol using generic providers.
This means more flexibility and choice when it comes to managing your features, and no vendor lock-in.

For the full technical details, see the OpenFeature protocol repository: `https://github.com/open-feature/protocol/`.

## How to use OFREP with Semaphore?
You need to have the [relay proxy](../relay-proxy/README.md) running in your infrastructure.
It exposes the OFREP endpoints:

| Endpoint                                  | Usage                                                          |
|-------------------------------------------|----------------------------------------------------------------|
| `POST /ofrep/v1/evaluate/flags/{key}`     | Evaluate a single flag (server-side SDKs).                     |
| `POST /ofrep/v1/evaluate/flags`           | Bulk evaluation of all flags (client-side SDKs).               |

See the [endpoints page](../relay-proxy/endpoints.md) and the OpenAPI documentation in
`cmd/relayproxy/docs/swagger.yaml` for the details.

Point the OFREP provider of your language at the base URL of the relay proxy (for example `http://localhost:1031`).
If you configured [API keys](../relay-proxy/configuration.md), pass the key to the provider as the `X-API-Key` header or as
an `Authorization: Bearer <key>` header (see the `headers` option of the example below).
The relay proxy does not implement `GET /ofrep/v1/configuration`.

### Example with the JavaScript SDK

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

Browser (`npm install @openfeature/web-sdk @openfeature/ofrep-web-provider`) uses the bulk endpoint and
`OpenFeature.setContext(...)` for the evaluation context:

```javascript
import { OpenFeature } from '@openfeature/web-sdk';
import { OFREPWebProvider } from '@openfeature/ofrep-web-provider';

await OpenFeature.setContext({ targetingKey: 'user-123' });
await OpenFeature.setProviderAndWait(new OFREPWebProvider({ baseUrl: 'http://localhost:1031' }));
const enabled = OpenFeature.getClient().getBooleanValue('my-flag', false);
```

Other languages work the same way, with the OFREP provider published by the OpenFeature project for your language
(see the [OpenFeature ecosystem](https://openfeature.dev/ecosystem)).

### Example with curl

```shell
curl 'http://localhost:1031/ofrep/v1/evaluate/flags/my-flag' -X POST \
  -H 'Content-Type: application/json' \
  --data-raw '{"context": {"targetingKey": "user-123"}}'
```

## Evaluation context
The OFREP context is the OpenFeature evaluation context, see [evaluation context](../concepts/evaluation-context.md)
for the reserved keys (such as `gofeatureflag`) understood by the relay proxy.

## Not available through OFREP
In-process evaluation inside the application (as done by language-specific providers) is not available, apart from the Go module.
Flag-usage events are produced by the relay proxy at evaluation time; see [flag usage tracking](../tracking/flag-usage-tracking.md).
