# Linter

Use [`semaphore-cli`](cli.md) `lint` to verify that Semaphore can parse and validate your flag configuration file.

> [!TIP]
> Run `semaphore-cli lint` in CI/CD pipelines to catch invalid flag configurations before deploying.

## Install the Command Line

See the [installation guide](cli.md) to install `semaphore-cli`.

## Use the linter

Pass the path to your flag configuration file and its format (`yaml`, `json`, or `toml`):

```shell
semaphore-cli lint /input/my-flag-config.yaml --format=yaml
```

## Use the linter in CI

Build `semaphore-cli` in your pipeline and run it against your flag files:

```yaml
name: "Lint flags"
on:
  pull_request:
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          repository: BlackbirdWorks/semaphore
          path: semaphore
      - uses: actions/setup-go@v5
        with:
          go-version-file: semaphore/go.mod
      - run: cd semaphore && go build -o ../semaphore-cli ./cmd/cli
      - uses: actions/checkout@v4
        with:
          path: flags
      - run: ./semaphore-cli lint flags/path/to/your/config.yaml --format=yaml
```

## Lint feature flags programmatically

You can also validate a flag file in Go using the flag format model in the [Go module](../go-module/getting-started.md).
