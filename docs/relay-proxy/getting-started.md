# Getting started

## Overview
The **relay proxy** loads your flag files from a remote or local source and serves flag evaluations to your
applications over HTTP ([OFREP](../openfeature/ofrep.md) and the relay proxy API).

This guide starts a local relay proxy using a file retriever.

## Build

```shell
task build-relayproxy   # binary: out/bin/semaphore
```

See [installation](install.md) for the other options (Docker, FIPS).

## Create a flag file

```yaml
# flags.yaml
new-checkout:
  variations:
    enabled: true
    disabled: false
  targeting:
    - name: beta users
      query: beta eq true
      variation: enabled
  defaultRule:
    variation: disabled
```

## Create the relay proxy configuration

```yaml
# semaphore-proxy.yaml
pollingInterval: 1000 # poll the file every second
retrievers:
  - kind: file
    path: ./flags.yaml
```

## Start

```shell
./out/bin/semaphore --config semaphore-proxy.yaml
```

What this does:
- Reads `semaphore-proxy.yaml` and loads flags via the file retriever.
- Polls the file regularly for changes.
- Exposes evaluation APIs on port `1031` by default.

## Evaluate a flag

```shell
curl 'http://localhost:1031/ofrep/v1/evaluate/flags/new-checkout' -X POST \
  -H 'Content-Type: application/json' \
  --data-raw '{"context": {"targetingKey": "user-123", "beta": true}}'
```

Response:

```json
{
  "key": "new-checkout",
  "value": true,
  "reason": "TARGETING_MATCH",
  "variant": "enabled",
  "metadata": {
    "evaluatedRuleName": "beta users",
    "gofeatureflag_cacheable": true,
    "semaphore_cacheable": true
  }
}
```

Without `"beta": true` in the context, the flag returns `false` with reason `DEFAULT`.

## Next steps

- Connect an application using a standard OpenFeature SDK with [OFREP](../openfeature/ofrep.md).
- See the [configuration reference](configuration.md) for all available options.
