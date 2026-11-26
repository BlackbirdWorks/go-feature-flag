# Config

`semaphore-cli config` checks the version 2 [bootstrap file](../relay-proxy/bootstrap-config.md) with the strict loader that the
relay will use.

> [!NOTE]
> The relay does not read the version 2 format yet: it still starts from its current configuration. A valid file and the
> effective print describe what the relay will run once it switches to version 2, not what it runs today.

## Validate

```shell
semaphore-cli config validate bootstrap.yaml
```

- Prints `bootstrap.yaml: valid` and exits 0, or prints the first error with the file, the path of the key and the line, and exits 2:

```text
bootstrap.yaml: connections.control.bogus (line 6): unknown key
```

- A version 1 file fails with `version 2 required`. Unknown keys, bad references, bad values and inline secrets are errors.
- Secret references (`fromEnv`, `fromFile`) are checked for their shape, not resolved.
- `--output json` prints `{"file", "valid", "error": {"path", "line", "message"}}` (the error only when invalid).
- A file that cannot be read exits 1.

## Print the effective configuration

```shell
semaphore-cli config print --effective bootstrap.yaml
```

Prints what the relay would run with: defaults applied (for example `pollInterval: 60s`) and the `SEMAPHORE_*` environment
overrides applied. Secrets and admin keys print as `[REDACTED]`; references print as written and are not resolved.

- `--effective` is required.
- `--output text` (default, YAML) or `--output json`.
- An invalid file exits 2 with the same error as `validate`.
