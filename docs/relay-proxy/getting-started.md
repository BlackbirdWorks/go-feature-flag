# Getting started

## Overview
The **relay proxy** reads flag sets from a control plane store (an S3 bucket or a directory), where a publisher
writes a manifest and the objects it lists, and serves flag evaluations to your applications over HTTP
([OFREP](../openfeature/ofrep.md) and the relay proxy API). It is configured by a
[version 2 bootstrap file](bootstrap-config.md); a version 1 file is refused with `version 2 required`.

This guide starts a local relay proxy on the example file store.

## Build

```shell
task build-relayproxy   # binary: out/bin/semaphore
```

See [installation](install.md) for the other options (Docker, FIPS).

## Start

`cmd/relayproxy/testdata/dockerhub-example` holds a bootstrap file, a file store (a manifest and its objects) and the
flag file they were made from:

```yaml
# bootstrap.yaml
version: 2
connections:
  control: {type: file, path: ./store}
controlPlane:
  connection: control
  manifest: {key: manifests/stable.yaml, channel: stable, pollInterval: 10s}
```

```shell
cd cmd/relayproxy/testdata/dockerhub-example
../../../../out/bin/semaphore --config bootstrap.yaml
```

What this does:
- Reads the bootstrap file (`--config` or `SEMAPHORE_CONFIG`) and builds the connection to the store.
- Polls the manifest every 10 seconds; a changed object is live after one poll, without a restart.
- Exposes the evaluation APIs on port `1031` and the probes `/livez`, `/readyz` and `/startupz` (see [probes](probes.md)).
- `SIGHUP` reloads the bootstrap file, `SIGTERM` stops the relay in order.

For your own flags, write flag files and flag-set documents and publish them with
[`semaphore-cli publish`](../tooling/publish.md) to an S3 bucket, then point `controlPlane.connection` at it.

## Evaluate a flag

```shell
curl 'http://localhost:1031/ofrep/v1/evaluate/flags/flag-only-for-admin' -X POST \
  -H 'Content-Type: application/json' -H 'X-API-Key: example-api-key' \
  --data-raw '{"context": {"targetingKey": "user-123", "admin": true}}'
```

The answer carries `"value": true` with the reason `TARGETING_MATCH`. A request without a key, or with an unknown one,
is answered with 401.

## Next steps

- Connect an application using a standard OpenFeature SDK with [OFREP](../openfeature/ofrep.md).
- See the [bootstrap file reference](bootstrap-config.md) for all available options.
