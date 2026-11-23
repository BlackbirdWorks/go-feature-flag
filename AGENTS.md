# AGENTS.md - Semaphore codebase guide

Guide for AI agents and developers working in this repository. Prefer the `Makefile` (`make help`).

## What this is

A self-hosted feature flag **relay proxy** plus the Go **engine** it is built on. Applications use the
standard OpenFeature SDKs over HTTP (OFREP / the relay proxy API). It is an independent derivative of
GO Feature Flag by Thomas Poignant (MIT); see `UPSTREAM.md` for the upstream baseline and the intentional
divergences (in-repo `rules/` engine, no WASM/TinyGo, no bundled provider SDKs), and `NOTICE` for attribution.

## Layout

- Root `.go` files: the Go engine, `package ffclient` (`feature_flag.go`, `variation*.go`, `config*.go`, `tracking.go`).
- `cmd/relayproxy/`: HTTP relay proxy (Echo, Zap logging, Prometheus metrics, OpenTelemetry). Swagger in `cmd/relayproxy/docs/`, Helm chart in `cmd/relayproxy/helm-charts/relay-proxy/`.
- `cmd/cli/`: command line (`evaluate`, `lint`, `generate`); `cmd/jsonschema-generator/`.
- `retriever/`, `exporter/`, `notifier/`: integration packages (flag sources, evaluation-data sinks, change notifications).
- `modules/core/`: separate Go module with flag, context and evaluation logic.
- `rules/`: vendored rules engine (separate module, own `LICENSE`, must be kept).
- `internal/`, `ffcontext/`, `model/`, `cmdhelpers/`, `utils/`, `testutils/`, `testdata/`.
- `openfeature/provider_tests/`: protocol compatibility tests of the relay proxy against real OpenFeature providers (Go, JS, Java, .NET); `make provider-tests`.
- `.schema/flag-schema.json`: JSON schema of the flag file.
- `docs/`: plain Markdown documentation (index in `docs/README.md`).

## Commands

Go workspace mode is not used by CI: export `GOWORK=off`.

```shell
export GOWORK=off
make build             # relay proxy, cli, jsonschema generator, all modules
make test-unit         # unit tests (root + modules/core + rules)
make test-integration  # testcontainers tests (Docker)
make test-e2e          # gopherstack e2e tests (Docker)
make test-shards-check # CI test groups must cover every package
make lint              # golangci-lint
make tidy-check        # go.mod / go.sum tidy
make swagger           # regenerate swagger after API annotation changes
```

## Conventions

- Tests: parallel table-driven tests (`t.Parallel()`), `testify/assert`; no `export_test.go`. Fix flaky tests at the cause (no rerun-until-green, skips or blind threshold bumps).
- Return errors, do not panic. Go module logging uses `slog` (`utils/fflog`); the relay proxy uses Zap.
- Ask before adding dependencies. Never commit secrets. Do not edit `vendor/` or `go.work` by hand.
- PR titles use Conventional Commits and commits are signed. Never force-push `main`.
- Do not add or alter licence headers; keep `LICENSE`, `rules/LICENSE` and `NOTICE` intact.

## Identifiers that must not be renamed

These are client or deployment contracts (a rename needs a coordinated migration):

- OFREP metadata key `gofeatureflag_cacheable`; evaluation-context key `gofeatureflag` (and its sub-keys).
- Prometheus subsystem `gofeatureflag`.
- Default relay proxy config file name `goff-proxy` (searched in `./`, `/goff/`, `/etc/opt/goff/`) and the environment variable prefixes.
- OpenTelemetry tracer and service names.
- Go module path `github.com/thomaspoignant/go-feature-flag` and all Go import paths (a later change renames the module).

## Entry points

`ffclient.Init()`, `cmd/relayproxy/main.go`, `cmd/cli/main.go`. Interfaces: `Retriever`, `Exporter`, `Notifier`.
