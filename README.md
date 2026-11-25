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
- No bundled provider SDKs: use the standard OpenFeature SDKs with OFREP, and read the docs at [blackbirdworks.github.io/semaphore](https://blackbirdworks.github.io/semaphore/) or in [`docs/`](docs/README.md).

## Quick start

Build the relay proxy and run it with a file retriever (requires Go, see `go.mod`):

```shell
make build-relayproxy        # binary: out/bin/semaphore

cat > flags.yaml <<'YAML'
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
YAML

cat > semaphore-proxy.yaml <<'YAML'
pollingInterval: 1000
retrievers:
  - kind: file
    path: ./flags.yaml
YAML

./out/bin/semaphore --config semaphore-proxy.yaml
```

Evaluate the flag through OFREP from another terminal:

```shell
curl -s http://localhost:1031/ofrep/v1/evaluate/flags/new-checkout -X POST \
  -H 'Content-Type: application/json' \
  --data-raw '{"context": {"targetingKey": "user-123", "beta": true}}'
# {"key":"new-checkout","value":true,"reason":"TARGETING_MATCH","variant":"enabled","metadata":{"evaluatedRuleName":"beta users","gofeatureflag_cacheable":true,"semaphore_cacheable":true}}
```

Then point an OpenFeature OFREP provider at `http://localhost:1031`, see [docs/openfeature/ofrep.md](docs/openfeature/ofrep.md).

## Development

```shell
export GOWORK=off
make build             # relay proxy, cli, jsonschema generator
make test-unit         # unit tests
make test-integration  # testcontainers tests (Docker)
make test-e2e          # end-to-end tests (Docker)
make lint
```

See [CONTRIBUTING.md](CONTRIBUTING.md) and [AGENTS.md](AGENTS.md).

## Documentation

Read the hosted documentation at [blackbirdworks.github.io/semaphore](https://blackbirdworks.github.io/semaphore/), or browse the plain Markdown index in [docs/README.md](docs/README.md) (covering the relay proxy, flag format, rollouts, retrievers, exporters, notifiers and the Go module).

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
