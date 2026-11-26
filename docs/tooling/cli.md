# Command Line

The Semaphore CLI (`semaphore-cli`) lets you work with Semaphore in your terminal.

Supported commands:

- [`evaluate`](evaluate.md): Evaluate feature flags directly in your terminal.
- [`lint`](linter.md): Validate a flag configuration file format.
- [`generate`](generate.md): Generate an OpenFeature flag manifest for the OpenFeature CLI **(experimental)**.
- [`publish`](publish.md): Publish flag files and flag-set documents to S3 behind a manifest, and verify the bucket.
- [`config`](config.md): Validate and print the effective version 2 bootstrap file of the relay.

## Install the Command Line

After the first release (see [Releasing](../RELEASING.md)), download `semaphore-cli_<version>_<Os>_<arch>.tar.gz` (`.zip` on Windows)
from [GitHub Releases](https://github.com/BlackbirdWorks/semaphore/releases), or use the container image
`ghcr.io/blackbirdworks/semaphore-cli:v<version>` (entrypoint `/semaphore-cli`; `semaphore-cli --version` prints the version).

## Build the Command Line

```shell
task build-cli   # binary: out/bin/semaphore-cli
```

The docs below refer to the binary as `semaphore-cli`; substitute `out/bin/semaphore-cli` when running a local build.
