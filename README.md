# Semaphore

A self-hosted feature flag **relay proxy** and Go **engine**. Applications evaluate flags over HTTP
with the standard [OpenFeature](https://openfeature.dev) SDKs and the OFREP provider
(OpenFeature Remote Evaluation Protocol); Go programs can also embed the engine directly.

Semaphore is derived from GO Feature Flag by Thomas Poignant (MIT), `https://github.com/thomaspoignant/go-feature-flag`.
See [UPSTREAM.md](UPSTREAM.md) for the upstream baseline.
This is an independent project: it is not affiliated with, sponsored by or endorsed by the GO Feature Flag project or its
maintainers, and it is unrelated to other products or services that share the name Semaphore.

## Compatibility with GO Feature Flag

The wire protocol is unchanged: the relay proxy serves the same HTTP endpoints (`/ofrep/v1/...`, `/v1/...`), so the
standard OpenFeature SDKs and the GO Feature Flag OpenFeature providers keep working against it, and the compatibility
tests of `openfeature/provider_tests` run those upstream providers against this relay proxy. The names that GO Feature
Flag used (`goff-proxy.yaml`, the `gofeatureflag` evaluation context key, the `gofeatureflag_cacheable` metadata, the
`X-GOFEATUREFLAG-VERSION` header, the `gofeatureflag_` metric prefix) are still accepted or emitted next to the Semaphore
ones. See the [migration table](docs/migration/rename-to-semaphore.md).

## How it differs from upstream

- The rules engine is vendored in-repo (`rules/`, a directory of the root Go module).
- No WASM / TinyGo build.
- No bundled provider SDKs: use the standard OpenFeature SDKs with OFREP, and read the docs at [semaphore.blackbirdworks.dev](https://semaphore.blackbirdworks.dev/) or in [`docs/`](docs/README.md).

## Quick start

Build the relay proxy and run it on the example file store (requires Go, see `go.mod`, and [Task](https://taskfile.dev)):

```shell
task build-relayproxy        # binary: out/bin/semaphore
cd cmd/relayproxy/testdata/dockerhub-example   # a version 2 bootstrap file and a file store with flags
../../../../out/bin/semaphore --config bootstrap.yaml
```

Evaluate a flag through OFREP from another terminal (the example flag set has the API key `example-api-key`):

```shell
curl -s http://localhost:1031/ofrep/v1/evaluate/flags/flag-only-for-admin -X POST \
  -H 'Content-Type: application/json' -H 'X-API-Key: example-api-key' \
  --data-raw '{"context": {"targetingKey": "user-123", "admin": true}}'
```

Publish your own flags with `semaphore-cli publish` to an S3 bucket, see [docs/relay-proxy/getting-started.md](docs/relay-proxy/getting-started.md).

Then point an OpenFeature OFREP provider at `http://localhost:1031`, see [docs/openfeature/ofrep.md](docs/openfeature/ofrep.md).

## Development

```shell
export GOWORK=off
task build             # relay proxy, cli, jsonschema generator
task test-unit         # unit tests
task test-integration  # testcontainers tests (Docker)
task test-e2e          # end-to-end tests (Docker)
task lint
```

See [CONTRIBUTING.md](CONTRIBUTING.md) and [AGENTS.md](AGENTS.md).

## Documentation

Read the hosted documentation at [semaphore.blackbirdworks.dev](https://semaphore.blackbirdworks.dev/), or browse the plain Markdown index in [docs/README.md](docs/README.md) (covering the relay proxy, flag format, rollouts, retrievers, exporters, notifiers and the Go module).

To preview or build the documentation site locally:

```shell
task docs-dev    # local dev server (hot reload)
task docs-build  # static build in docs/.vitepress/dist/
task docs-deploy # build locally and trigger GitHub Pages deployment
```

## License and attribution

Released under the MIT License, see [LICENSE](LICENSE). Semaphore is derived from GO Feature Flag,
Copyright (c) 2020 Thomas Poignant (MIT), `https://github.com/thomaspoignant/go-feature-flag`;
the `rules/` directory is derived from `https://github.com/nikunjy/rules`, Copyright (c) 2019 Nikunj Yadav
(MIT, see [rules/LICENSE](rules/LICENSE)). Modifications are Copyright (c) 2026 BlackbirdWorks under the
same license. See [NOTICE](NOTICE) and [UPSTREAM.md](UPSTREAM.md).
