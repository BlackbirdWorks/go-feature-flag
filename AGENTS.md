# AGENTS.md - the rules of the Semaphore repository

The single place to read before changing this repository (contributors and coding agents alike). Prefer the
`Taskfile.yml` (`task --list`). [CONTRIBUTING.md](CONTRIBUTING.md) covers issues and the pull request flow and points here.

## What this is

A self-hosted feature flag **relay proxy** plus the Go **engine** it is built on. Applications use the standard
OpenFeature SDKs over HTTP (OFREP / the relay proxy API). It is an independent derivative of GO Feature Flag by Thomas
Poignant (MIT); see `UPSTREAM.md` for the baseline and the intentional divergences, and `NOTICE` for attribution.

## Where things are

- Root `.go` files: the engine, `package ffclient` (`feature_flag.go`, `variation*.go`, `config*.go`, `tracking.go`).
- `cmd/relayproxy/`: relay proxy (`net/http` ServeMux, huma operations, `log/slog`, Prometheus, OpenTelemetry); the OpenAPI document is generated from the registered operations at runtime (`api.OpenAPI`), `cmd/relayproxy/api/testdata/openapi.golden.json` pins it.
- `cmd/cli/`, `cmd/jsonschema-generator/`: command line (`evaluate`, `lint`, `generate`) and the schema generator.
- `modules/core/`: flag, context, evaluation, `clock` (directories of the root module, not separate modules).
- `rules/`: the rules engine (query parser and evaluator, own `LICENSE`); `rules/parser` holds antlr generated files.
- `internal/cache/`: flag cache. `ffcontext/`, `model/`, `utils/`: public helper packages of the engine.
- `retriever/`, `exporter/`, `notifier/`: integrations (flag sources, evaluation-data sinks, change notifications).
- `testhelpers/` (mocks, `gopherstack` emulator helpers), `testdata/`: test helpers and fixtures.
- `openfeature/provider_tests/`: compatibility tests of the relay proxy against real OpenFeature providers.
- `.github/ci-scripts/`: CI helper scripts (test sharding, coverage, fuzz); `docs/`: Markdown documentation (`docs/README.md`) and inline VitePress site (`docs/.vitepress/`).
- `.schema/flag-schema.json`: JSON schema of the flag file.
- Entry points: `ffclient.New()`, `cmd/relayproxy/main.go`, `cmd/cli/main.go`; interfaces `Retriever`, `Exporter`, `Notifier`.

## Tests

- Table-driven and parallel: `t.Parallel()` at the top of the test and in every `t.Run`.
- testify only: `require` stops the test, `assert` continues; `t.Fatal*`/`t.Error*` are forbidden (lint).
- Every test and every table case asserts the outcome against an expected value in a `want...` field; "no error" alone is
  not enough when a concrete result exists.
- Black-box tests in `package x_test` by default; white-box tests that need unexported names live in `*_internal_test.go`.
  Never add `export_test.go`.
- No wall clock: read time through `clock.Now` (`modules/core/clock`), set it once per test package in `TestMain` with
  `clocktest` (fixed or stepping clock), and never reassign it in a test.
- Never call `require` off the test goroutine (goroutines, handlers): use `assert`.
- No `time.Sleep` as synchronisation: wait on a channel, `Eventually` or a signal.
- A test server keeps its listener open from the start (no pick-a-port-then-rebind).
- Fix a flaky test at its cause: no retries, skips or loosened thresholds.

## Design

- No mutable package-level state and no singletons (`gochecknoglobals`, `gochecknoinits`, `reassign` enforce it): state is
  owned by an object created at startup and passed in (`flag.Service`, the client, the registries).
- The only global is `clock.Now`; loggers and clients are injected. Everything logs through `log/slog`: the engine and the relay proxy through
  a `*slog.Logger` (the relay proxy builds its own with `cmdhelpers/cmdlog`, `json` or `logfmt` handler). Zap is
  forbidden (`depguard`). Log with `logger.LogAttrs(ctx, level, msg, slog.String(...), ...)` and typed attributes on hot
  paths (the `any` variadic form allocates even when the level is disabled), guard expensive values with `logger.Enabled`,
  and wrap secrets in `log.Secret`.
- Use concrete types on the evaluation path.
- Return errors, do not panic. Sentinel or typed errors, never ad-hoc `errors.New` at the call site (`err113`).
- Ask before adding dependencies. Never commit secrets. Do not edit `vendor/` or `go.work` by hand.

## Performance

- The evaluation hot path must not regress: `rules/`, `modules/core/flagdef`, `modules/core/ffcontext`, `internal/cache`, the
  root `variation*.go`, and the relay evaluation handlers (OFREP and eval). Allocations per operation must not increase.
- Some code there keeps a faster hand-written form under a `//nolint` with a measured reason (for example loops instead of
  `slices.Contains`, exact error type checks instead of `errors.As`). Do not modernise it.
- Never run lint autofix or `golangci-lint fmt` across the tree blindly: apply it per linter and per directory and review
  the hot path diffs by hand.
- Benchmark before and after with test binaries built once and run interleaved on pinned CPUs:

```shell
go test -c -o base/parser.test ./rules/parser        # same for ./modules/core/flagdef, ./internal/cache
go test -c -tags=bench -o base/root.test .           # scenario benchmarks of the root package
taskset -c 2,3 ./base/parser.test -test.run '^$' -test.bench . -test.benchmem -test.count 10 -test.cpu 1 > base.txt
taskset -c 2,3 ./new/parser.test  -test.run '^$' -test.bench . -test.benchmem -test.count 10 -test.cpu 1 > new.txt
benchstat base.txt new.txt                           # alternate base and new runs, and check `uptime` (load < 3) first
```

## Compatibility

- Clients are OpenFeature/OFREP providers over HTTP: responses, headers, config keys, CLI output and the legacy aliases
  below stay stable unless a change is decided explicitly. `openfeature/provider_tests` is the guard (`task provider-tests`).
- Struct field order is output order for JSON, CSV and Parquet exports and the CLI: the exporter events, the manifest and the
  evaluation results keep their order under a reasoned `//nolint:govet` (fieldalignment).
- The Go API is internal (nothing imports this module): it may change.

### Names: Semaphore first, GO Feature Flag still accepted on the wire

The names below are client contracts. The Semaphore name is canonical, the legacy GO Feature Flag name is an alias
that keeps working (one warning per owner (client, CLI command) and kind when it is used, `modules/core/legacy`). Do not remove an alias
without a coordinated migration, see `docs/migration/rename-to-semaphore.md` for the full table. The relay reads one version 2
bootstrap file: its configuration file, folder and environment aliases are gone.

- Evaluation-context key `semaphore` (and its sub-keys), legacy `gofeatureflag`; OFREP metadata `semaphore_cacheable` and `gofeatureflag_cacheable`; headers `X-SEMAPHORE-VERSION` and `X-GOFEATUREFLAG-VERSION`.
- Prometheus prefix `semaphore_`, legacy `gofeatureflag_` behind `observability.metrics.legacyNames`. OpenTelemetry service name and scope default to `semaphore`.
- Wire paths, JSON field names and the flag file format are not renamed; the provider tests in `openfeature/provider_tests` run the upstream providers and must keep passing.
- Go module path `github.com/blackbirdworks/semaphore` (a single Go module; `openfeature/provider_tests/go-integration-tests` is the only other `go.mod`).

## Lint

- `task lint` must be clean (golangci-lint plus `mulint` through `go vet -vettool`, version pinned in `Taskfile.yml`; see `.golangci.yml`, every linter is enabled
  or listed there with a reason). Run `task tidy-check` (tree stays clean) too.
- No blanket or path-wide exclusions. A suppression is one line, `//nolint:<linter> // <specific reason>`, and unused ones
  are removed (`nolintlint`). Generated files are excluded by their `Code generated` header only.

## Commits and pull requests

- Commits are signed; pull request titles and commit subjects follow Conventional Commits.
- `main` is squash-merged. Never force-push or rebase a shared branch.
- Verify every review finding, then fix it or answer it before merging.
- Keep pushes few: batch a round of changes and verify locally first:

```shell
export GOWORK=off
go build ./... && go vet -tags=docker,bench ./...
task lint && task tidy-check && task test-shards-check && task test-ci-scripts
go test -race -shuffle=on -count=1 ./...
task test-unit && task test-integration && task test-e2e   # integration and e2e need Docker
task coverage COVERAGE_MIN=92                              # coverage floor
task provider-tests
```

## Upstream

- The project is an independent derivative: never push, open issues or pull requests, or comment on the upstream projects,
  and never publish to their registries. Write upstream names in backticks, without links or mentions.
- Provenance lives in `UPSTREAM.md` and `NOTICE`; keep `NOTICE`, `LICENSE` and `rules/LICENSE` intact and do not alter licence headers.
- Bugs found in this project are filed in this repository.

## Releases

Tags, releases and images are created only by the release workflow, run by hand with a bump type (minor or major); never tag or publish by hand, except that an owner may push a pre-release tag (`-alpha.N`, `-beta.N`, `-rc.N`) which the same workflow publishes. See [docs/RELEASING.md](docs/RELEASING.md).

## Documentation

- `docs/` is both plain GitHub Markdown and the VitePress site at `https://semaphore.blackbirdworks.dev/` (auto-deployed on stable releases; run `task docs-dev` to preview, `task docs-deploy` to publish).
- **Style**: be concise and direct. Use simple, plain language, prefer bullet lists over long prose, and show quick code snippets for examples.
- Keep `docs/**/*.md` valid plain Markdown (no VitePress frontmatter outside `docs/index.md`, relative `.md` links, no blank lines inside HTML `<table>` blocks).
- When adding, removing, or renaming a page, update `docs/README.md` and the sidebar in `docs/.vitepress/config.mts`, then check with `task docs-build`.
- **No machine specs**: keep benchmark/perf hardware vague (e.g. "commodity 4-core / 8-thread Linux machine"). Never include CPU models, exact RAM, hostnames, local paths, or kernel patch versions.
