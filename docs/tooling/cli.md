# Command Line

The Semaphore CLI (`semaphore-cli`) lets you work with Semaphore in your terminal.

Supported commands:

- [`evaluate`](evaluate.md): Evaluate feature flags directly in your terminal.
- [`lint`](linter.md): Validate a flag configuration file format.
- [`generate`](generate.md): Generate an OpenFeature flag manifest for the OpenFeature CLI **(experimental)**.
- [`publish`](publish.md): Publish flag files and flag-set documents to S3 behind a manifest, and verify the bucket.
- [`config`](config.md): Validate and print the effective version 2 bootstrap file of the relay.

## Output streams and exit codes

stdout carries results only; stderr carries every diagnostic.

| Command | stdout (result) | stderr (diagnostics) |
|---|---|---|
| `evaluate` | the evaluation result (JSON) | the final error line |
| `lint` | `Valid Semaphore configuration` | the findings (one `ERROR` line each) and the final error line |
| `generate manifest` | `Manifest has been created` | warnings and notes (for example `flag ignored: no default value provided`), the final error line |
| `publish`, `config` | the result, or with `--output json` exactly one JSON value (see [publish](publish.md#streams)) | the final error line |

- The final `error executing command` line of every command is on stderr, as are Cobra's usage and its own errors.
- With `--output json` every failure of `publish` and `config`, argument and flag errors included, writes one JSON error object on
  stdout (a `publish verify` report and a `config validate` report are their own JSON document).
- Every failure exits with 1, except where a command defines more: `publish` and `config` use 2, 3, 4 and 5 (see
  [publish](publish.md#exit-codes)).

## Install the Command Line

After the first release (see [Releasing](../RELEASING.md)), download `semaphore-cli_<version>_<Os>_<arch>.tar.gz` (`.zip` on Windows)
from [GitHub Releases](https://github.com/BlackbirdWorks/semaphore/releases), or use the container image
`ghcr.io/blackbirdworks/semaphore-cli:v<version>` (entrypoint `/semaphore-cli`; `semaphore-cli --version` prints the version).

## Build the Command Line

```shell
task build-cli   # binary: out/bin/semaphore-cli
```

The docs below refer to the binary as `semaphore-cli`; substitute `out/bin/semaphore-cli` when running a local build.
