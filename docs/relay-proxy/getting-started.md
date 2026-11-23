# Getting started

## Overview
The **relay proxy** loads your flag files from a remote or local source and serves flag evaluations to your
applications over HTTP ([OFREP](../openfeature/ofrep.md) and the relay proxy API).

This page starts a relay proxy locally with a file retriever.

## Build

```shell
make build-relayproxy   # binary: out/bin/relayproxy
```

See [installation](install.md) for the other options (Docker, FIPS).

## Create a flag file

```yaml
# flags.goff.yaml
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
# goff-proxy.yaml
pollingInterval: 1000 # poll the file every second
retrievers:
  - kind: file
    path: ./flags.goff.yaml
```

## Start

```shell
./out/bin/relayproxy --config goff-proxy.yaml
```

What is happening?
- The relay proxy reads the configuration file and uses the file retriever to load the flags.
- It polls the file regularly to pick up changes.
- A set of APIs is available to evaluate the flags (port `1031` by default).

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
    "gofeatureflag_cacheable": true
  }
}
```

Without `"beta": true` in the context, the flag returns `false` with reason `DEFAULT`.

Next, connect an application with a standard OpenFeature SDK, see [OFREP](../openfeature/ofrep.md),
and look at the [configuration reference](configuration.md) for all the options.
