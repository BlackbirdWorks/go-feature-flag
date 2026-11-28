# Flag Set

## Overview

A **flag set** is an isolated group of feature flags with its own API keys. It is defined in a flag-set document that
[`semaphore-cli publish`](../tooling/publish.md) writes to the control plane store, and it lists the flag objects (and
the segment objects) it serves:

```yaml
apiVersion: semaphore/v1
kind: FlagSet
flagSets:
  - name: checkout
    sources: [checkout-flags]
    apiKeys: [8d3b5f0e-6a4f-4c8e-9c3e-2f2f5d1a7b10]
```

- **Sources:** `sources` names flag objects of the manifest. A later source wins on a duplicate flag key.
- **API key routing:** a request carries an API key, which routes the evaluation to the matching flag set (a flag set can have several keys).
- **Complete isolation:** flag sets do not inherit flags or settings from one another.
- **Shared pipelines:** exporters and notifiers belong to the relay, not to a flag set, see [events and notifiers](events-and-notifiers.md).
- **Public flag set:** a flag set with no `apiKeys` is served to requests without a key, see [public flags](../relay-proxy/bootstrap-config.md#public-flags).

The full format is in the [flag-set document reference](../reference/schema.md#flag-set-document).

## When to use flag sets

- **Team isolation:** let separate teams (such as frontend and backend) manage their own flags independently, even reusing the same flag key without collisions.
- **Environment separation:** keep development, staging, and production flags isolated.
- **Multi-tenant applications:** give each customer or organization isolated flag configs.

## How to access a flag set

### Using API keys

Configure each OpenFeature provider with an API key belonging to the target flag set.

### Request routing

1. The relay proxy reads the API key from the `X-API-Key` or `Authorization` header.
2. It looks up the flag set associated with that key.
3. The evaluation runs against that flag set's flags and returns the result.

### Flag change streams

WebSocket and SSE flag change streams are scoped to the API key's flag set: a connection receives events only for its own flag set. Open one connection per flag set to watch multiple sets. See [flag change streams](../relay-proxy/endpoints.md#flag-change-streams).

## Changing flag sets at runtime

The relay proxy polls the manifest of the control plane. A new generation applies without a restart:

- **Adding a flag set:** it starts serving with the generation.
- **Removing a flag set:** it stops serving and its API keys are refused; the streams opened with them close.
- **Changing keys or sources:** applied live to the flag set.

A generation that fails validation is rejected whole and the relay keeps the last known good one, see [live reload](live-reload.md).
