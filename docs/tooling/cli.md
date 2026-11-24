# Command Line

The Semaphore Command Line is a CLI tool to interact with Semaphore in your terminal.

It offers a variety of commands to interact with Semaphore, for now the supported commands are:
- [`evaluate`](evaluate.md) to evaluate feature flags directly in your terminal
- [`lint`](linter.md) to validate a configuration file format.
- [`generate`](generate.md) to generate the OpenFeature flag manifest for OpenFeature cli **(experimental)**.

## Install the Command Line

After the first release (see [Releasing](../RELEASING.md)), download `semaphore-cli_<version>_<Os>_<arch>.tar.gz`
from the [GitHub Releases](https://github.com/BlackbirdWorks/semaphore/releases), or use the image
`ghcr.io/blackbirdworks/semaphore-cli:v<version>` (its entrypoint is `/semaphore-cli`; `semaphore-cli --version` prints the version).

## Build the Command Line

```shell
make build-cli   # binary: out/bin/semaphore-cli
```

The docs below call the binary `semaphore-cli`; use `out/bin/semaphore-cli` in the examples.
