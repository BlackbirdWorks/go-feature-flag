# Semaphore Command Line

# What is Semaphore Command Line?

The Semaphore Command Line is a CLI tool to interact with Semaphore in your terminal.  
For now it supports the following commands:

- `evaluate` to evaluate feature flags directly in your terminal
- `lint` to validate a configuration file format.
- `generate manifest` to create an OpenFeature flag manifest.
- `publish` to publish flag files and flag-set documents to S3 behind a manifest (`publish verify` checks the bucket).
- `config validate` and `config print --effective` to check the version 2 bootstrap file of the relay.

See [publish](../../docs/tooling/publish.md) and [config](../../docs/tooling/config.md).

## How to build the cli

```shell
make build-cli   # from the repository root; binary: out/bin/semaphore-cli
```

The examples below call the binary `semaphore-cli`; use `out/bin/semaphore-cli`.

# How to use the command line

**`semaphore-cli`**  is a command line tool.

## How to evaluate a flag

The evaluate command allows you to evaluate a feature flag or inspect the configuration of your retriever using
`--check-mode`

```shell
semaphore-cli evaluate [OPTIONS]
```

### Key Flags

| Flag           | Shorthand | Description                                                                                                                     | Default |
|----------------|-----------|---------------------------------------------------------------------------------------------------------------------------------|---------|
| `--kind`       | k         | Kind of configuration source. Determines where to read your flags from                                                          | file    |
| `--config`     | c         | Path to the local flag configuration file (⚠️ deprecated, use `--path` instead)                                                 | ""      |
| `--format`     | f         | Format of your input file (YAML, JSON or TOML)                                                                                  | yaml    |
| `--flag`       |           | Name of the flag to evaluate                                                                                                    | ""      |
| `--timeout`    |           | Timeout in seconds to access your configuration file                                                                            | 0       |
| `--ctx`        |           | Evaluation context as a json string                                                                                             | {}      |
| `--check-mode` |           | Check only mode - when set, the command will not perform any <br/>evaluation and returns the configuration of spanned retriever | false   |

Supported values for `--kind` are:

- `file`
- `http`
- `github`
- `gitlab`
- `s3`
- `googleStorage`
- `configmap` (kubernetes)
- `mongodb`
- `bitbucket`
- `azureBlobStorage`
- `postgresql`

**Caution**: The `redis` retriever is not supported by this command.

### Retriever specific flags

The aforementioned `--kind` parameter is used to determine the retriever to use. The semantic meaning of other flags
depends on that one, for
example `--path` parameter is used to specify local file when `--kind` being `file` but when `--kind` being `github` it
is used to specify
the path to the remote file.

#### File

| Flag     | Description                               | Default |
|----------|-------------------------------------------|---------|
| `--path` | Path to the local flag configuration file | ""      |

#### HTTP

| Flag       | Description                                                         | Default |
|------------|---------------------------------------------------------------------|---------|
| `--url`    | URL of the remote flag configuration file                           | ""      |
| `--method` | HTTP method to access your configuration file on HTTP               | GET     |
| `--body`   | Http body to access your configuration file on HTTP                 | ""      |
| `--header` | Header to add to the request. Supported formats are `k:v` and `k=v` | ""      |

#### GitHub

| Flag                | Description                                                                                                       | Default |
|---------------------|-------------------------------------------------------------------------------------------------------------------|---------|
| `--repository-slug` | Name of the repository                                                                                            | ""      |
| `--branch`          | Git branch name                                                                                                   | ""      |
| `--auth-token`      | Authentication token to access your configuration file                                                            | ""      |
| `--github-token`    | Authentication token to access your configuration file on GitHub<br/> (⚠️ deprecated, use `--auth-token` instead) | ""      |
| `--path`            | Path to the remote flag configuration file inside github repository                                               | ""      |

#### GitLab

| Flag                | Description                                                         | Default |
|---------------------|---------------------------------------------------------------------|---------|
| `--base-url`        | Base URL of your configuration file on Gitlab                       | ""      |
| `--repository-slug` | Name of the repository                                              | ""      |
| `--branch`          | Git branch name                                                     | ""      |
| `--path`            | Path to the remote flag configuration file inside gitlab repository | ""      |

#### BitBucket

| Flag                | Description                                                            | Default |
|---------------------|------------------------------------------------------------------------|---------|
| `--base-url`        | Base URL of your configuration file on BitBucket                       | ""      |
| `--repository-slug` | Name of the repository                                                 | ""      |
| `--branch`          | Git branch name                                                        | ""      |
| `--path`            | Path to the remote flag configuration file inside bitbucket repository | ""      |

#### S3

| Flag       | Description        | Default |
|------------|--------------------|---------|
| `--bucket` | Name of the bucket | ""      |
| `--item`   | Item of the bucket | ""      |

#### Google Storage

| Flag       | Description          | Default |
|------------|----------------------|---------|
| `--bucket` | Name of the bucket   | ""      |
| `--object` | Object of the bucket | ""      |

#### ConfigMap (Kubernetes)

| Flag           | Description                | Default   |
|----------------|----------------------------|-----------|
| `--namespace`  | Namespace of the ConfigMap | "default" |
| `--config-map` | Name of the ConfigMap      | ""        |
| `--key`        | Key of the ConfigMap       | ""        |

#### MongoDB

| Flag           | Description                                         | Default |
|----------------|-----------------------------------------------------|---------|
| `--uri`        | URI of your configuration file                      | ""      |
| `--database`   | Database name of your configuration file on mongodb | ""      |
| `--collection` | Collection of your configuration file on mongodb    | ""      |

#### Azure Blob Storage

| Flag             | Description                 | Default |
|------------------|-----------------------------|---------|
| `--account-name` | Name of the storage account | ""      |
| `--account-key`  | Key of the storage account  | ""      |
| `--container`    | Name of the container       | ""      |
| `--object`       | Name of the object blob     | ""      |

#### PostgreSQL

| Flag       | Description                                        | Default |
|------------|----------------------------------------------------|---------|
| `--uri`    | URI of your configuration file                     | ""      |
| `--table`  | Table of your configuration file                   | ""      |
| `--column` | Column mapping to add. Supported format is `c1:c2` | ""      |

As mentioned above the `--config` flag is deprecated and we encourage you to use the `--path` flag instead. For example
the following command:

```shell
semaphore-cli evaluate --config="<location_of_your_flag_configuration_file>" --flag="<name_of_your_flag_to_evaluate>" --ctx='<evaluation_ctx_as_json_string>'
```

may be replaced by:

```shell
semaphore-cli evaluate --kind="file" --path="<location_of_your_flag_configuration_file>" --flag="<name_of_your_flag_to_evaluate>" --ctx='<evaluation_ctx_as_json_string>'
```

## How to lint a configuration file

```shell
semaphore-cli lint <location_of_your_flag_configuration_file> --format="<yaml or json or toml>"
```

# License

View [license](https://github.com/BlackbirdWorks/semaphore/blob/main/LICENSE) information for the software
contained in this image.

## How can I contribute?

This project is open for contribution, see
the [contributor's guide](https://github.com/BlackbirdWorks/semaphore/blob/main/CONTRIBUTING.md) for some helpful
tips.
