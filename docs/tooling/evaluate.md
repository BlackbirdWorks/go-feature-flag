# Evaluate feature flags in your terminal

Use `semaphore-cli evaluate` to inspect which variation a flag resolves to for debugging, testing, or CI/CD pipelines.

> [!TIP]
> You can also run `evaluate` in CI/CD pipelines to gate steps on feature flags.

## Install the Command Line

See the [installation guide](cli.md) to install `semaphore-cli`.

## Use the evaluate command in your terminal

```shell
semaphore-cli evaluate \
  --kind="file" \
  --path="<location_of_your_flag_configuration_file>" \
  --format="yaml" \
  --flag="<name_of_your_flag_to_evaluate>" \
  --ctx='<evaluation_ctx_as_json_string>'
```

| Flag | Description |
|---|---|
| `--kind` | Retriever kind used to read the configuration (`file`, `http`, `github`, `gitlab`, `bitbucket`, `s3`, `googleStorage`, `configmap`, `mongodb`, `redis`, `azureBlobStorage`, `postgresql`). Default: `file`. |
| `--path` | **(mandatory for `file`)** Location of your configuration file (`--config` is a deprecated alias). |
| `--ctx` | **(mandatory)** Evaluation context in JSON format (e.g., `{"targetingKey":"123"}`). |
| `--format` | Format of your configuration file (`yaml`, `json`, or `toml`). Default: `yaml`. |
| `--flag` | Name of the flag to evaluate. If omitted, all flags are evaluated. |
