# Command Line

The Semaphore CLI (`semaphore-cli`) lets you work with Semaphore in your terminal.

Supported commands:

- [`evaluate`](evaluate.md): Evaluate feature flags directly in your terminal.
- [`lint`](linter.md): Validate a flag configuration file format.
- [`generate`](generate.md): Generate an OpenFeature flag manifest for the OpenFeature CLI **(experimental)**.

## Install the Command Line

After the first release (see [Releasing](../RELEASING.md)), download `semaphore-cli_<version>_<Os>_<arch>.tar.gz` (`.zip` on Windows)
from [GitHub Releases](https://github.com/BlackbirdWorks/semaphore/releases), or use the container image
`ghcr.io/blackbirdworks/semaphore-cli:v<version>` (entrypoint `/semaphore-cli`; `semaphore-cli --version` prints the version).

## Build the Command Line

```shell
task build-cli   # binary: out/bin/semaphore-cli
```

The docs below refer to the binary as `semaphore-cli`; substitute `out/bin/semaphore-cli` when running a local build.
