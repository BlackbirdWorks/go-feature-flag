# Linter

Ensuring the accuracy of your configuration is vital for the **Semaphore** to function as expected.
This is why you can lint your configuration file using the [`go-feature-flag-cli`](cli.md), a command line tool that allow to interact with Semaphore in your terminal. The `lint` command validates whether a flag file can be parsed by **Semaphore**.

> [!TIP]
> We recommend you to use this command line in your CI/CD pipelines to avoid any unforeseen issues.

## Install the Command Line

Check the [installation guide](cli.md) to install the `go-feature-flag-cli`.

## Use the linter

```shell
./go-feature-flag-cli lint /input/my-go-feature-flag-config.goff.yaml --format=yaml
```

You have to pass the location of your configuration file and the format of your current configuration file _(available formats are `yaml`, `json`, `toml`)_.

## Use the linter in your CI (continuous integration)

Build the command line in your pipeline and run it against your flag files:

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
          repository: BlackbirdWorks/go-feature-flag
          path: goff
      - uses: actions/setup-go@v5
        with:
          go-version-file: goff/go.mod
      - run: cd goff && go build -o ../goff-cli ./cmd/cli
      - uses: actions/checkout@v4
        with:
          path: flags
      - run: ./goff-cli lint flags/path/to/your/config.yaml --format=yaml
```

## Lint Feature Flags Programmatically
You can also lint/validate a flag file programmatically from Go, by using the flag format model of the Go module
(see the [Go module docs](../go-module/getting-started.md)).
