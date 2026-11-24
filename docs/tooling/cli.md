# Command Line

The Semaphore Command Line is a CLI tool to interact with Semaphore in your terminal.

It offers a variety of commands to interact with Semaphore, for now the supported commands are:
- [`evaluate`](evaluate.md) to evaluate feature flags directly in your terminal
- [`lint`](linter.md) to validate a configuration file format.
- [`generate`](generate.md) to generate the OpenFeature flag manifest for OpenFeature cli **(experimental)**.

## Build the Command Line

```shell
make build-cli   # binary: out/bin/semaphore-cli
```

The docs below call the binary `semaphore-cli`; use `out/bin/semaphore-cli` in the examples.
