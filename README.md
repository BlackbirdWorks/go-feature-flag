# Semaphore

A self-hosted feature flag **relay proxy** and Go **engine**. Applications evaluate flags over HTTP
with the standard [OpenFeature](https://openfeature.dev) SDKs and the OFREP provider
(OpenFeature Remote Evaluation Protocol); Go programs can also embed the engine directly.

Semaphore is derived from GO Feature Flag by Thomas Poignant (MIT), `https://github.com/thomaspoignant/go-feature-flag`.
See [UPSTREAM.md](UPSTREAM.md) for the upstream baseline.

## How it differs from upstream

- The rules engine is vendored in-repo (`rules/`).
- No WASM / TinyGo build.
- No bundled provider SDKs and no website: use the standard OpenFeature SDKs with OFREP, and the Markdown docs in [`docs/`](docs/README.md).

## Quick start

Build the relay proxy and run it with a file retriever (requires Go, see `go.mod`):

```shell
make build-relayproxy        # binary: out/bin/relayproxy

cat > flags.goff.yaml <<'YAML'
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

cat > goff-proxy.yaml <<'YAML'
pollingInterval: 1000
retrievers:
  - kind: file
    path: ./flags.goff.yaml
YAML

./out/bin/relayproxy --config goff-proxy.yaml
```

Evaluate the flag through OFREP from another terminal:

```shell
curl -s http://localhost:1031/ofrep/v1/evaluate/flags/new-checkout -X POST \
  -H 'Content-Type: application/json' \
  --data-raw '{"context": {"targetingKey": "user-123", "beta": true}}'
# {"key":"new-checkout","value":true,"reason":"TARGETING_MATCH","variant":"enabled","metadata":{"evaluatedRuleName":"beta users","gofeatureflag_cacheable":true}}
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

[docs/README.md](docs/README.md) indexes the relay proxy, flag format, rollouts, retrievers, exporters, notifiers and the Go module.

## License and attribution

Released under the MIT License, see [LICENSE](LICENSE). Semaphore is derived from GO Feature Flag,
Copyright (c) 2020 Thomas Poignant (MIT), `https://github.com/thomaspoignant/go-feature-flag`;
the `rules/` directory is derived from `https://github.com/nikunjy/rules`, Copyright (c) 2019 Nikunj Yadav
(MIT, see [rules/LICENSE](rules/LICENSE)). Modifications are Copyright (c) 2026 BlackbirdWorks under the
same license. See [NOTICE](NOTICE) and [UPSTREAM.md](UPSTREAM.md).
