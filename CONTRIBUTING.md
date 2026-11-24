# Contributing to Semaphore

Thank you for considering a contribution. Please read the [Code of Conduct](CODE_OF_CONDUCT.md) first.

## Issues

Use the issue templates in this repository for bugs and feature requests. Include steps to
reproduce, expected and actual behaviour, the version or commit, and relevant logs. Report
security problems privately, see [SECURITY.md](SECURITY.md).

## Pull requests

1. Fork the repository (or create a branch if you have write access) from `main`.
2. Make your change, with tests and, for user-facing changes, an update of [`docs/`](docs/README.md).
3. Make sure `make lint` and the tests below pass.
4. Open a pull request against `main` of this repository and fill in the pull request template.

Rules enforced by CI and review:

- The pull request **title** must follow [Conventional Commits](https://www.conventionalcommits.org/)
  (for example `fix: handle empty targeting key`); the `Lint PR title` workflow checks it.
- Commits must be **signed**; see [AGENTS.md](AGENTS.md#commits-and-pull-requests) for the rest of the rules.
- Do not remove the GO Feature Flag names that are still accepted, they are listed in [AGENTS.md](AGENTS.md#names-semaphore-first-go-feature-flag-still-accepted).

## Development setup

Everything is driven by the [`Makefile`](Makefile) (`make help` lists the targets). The repository is a
single-module Go repository (`modules/core` and `rules` are directories of the root module) that CI builds with the Go workspace
disabled, so export `GOWORK=off` when you do not use a `go.work` file:

```shell
export GOWORK=off
make build            # builds the relay proxy, cli and jsonschema generator into out/bin/
make test-unit        # unit tests of all modules, no Docker
make test-integration # testcontainers tests, needs Docker
make test-e2e         # gopherstack-backed end-to-end tests, needs Docker
make lint             # golangci-lint on all modules
make tidy-check       # go.mod / go.sum must be tidy
make swagger          # regenerate cmd/relayproxy/docs after changing API annotations
```

If you created a `go.work` with `make workspace-init` before `modules/core` and `rules` were folded into the root module,
run `go work edit -dropuse=./modules/core -dropuse=./rules` once from the repository root (or delete `go.work` and run
`make workspace-init` again); otherwise Go fails reading the removed go.mod files.

Optional: `pre-commit install` runs `gofumpt` and the import-order linter before each commit.

### Tests and project rules

Every change needs tests. The test style, the design, performance, compatibility and lint rules are in
[AGENTS.md](AGENTS.md), the single place for them.

## License

By contributing, you agree that your contributions are licensed under the [LICENSE](LICENSE) of the project.
See [NOTICE](NOTICE) for the attribution of the upstream work.
