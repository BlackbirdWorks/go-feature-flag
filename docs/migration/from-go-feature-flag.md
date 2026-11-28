# From the go-feature-flag relay proxy (config v1)

The relay reads one format: the version 2 [bootstrap file](../relay-proxy/bootstrap-config.md). This page lists what is
gone, what replaced it, and the names that stay on the wire. There is no conversion tool: write a version 2 file.

## What is gone

- The version 1 configuration file (`goff-proxy.yaml` and its folders), the `GOFF*` environment variables, and the
  `retrievers`, `exporter`, `flagsets` and `listen` style keys. A file without `version: 2` is refused with `version 2 required`.
- Studio.
- The AWS Lambda mode and artifact (`server.mode`).
- Every retriever of the relay except the control plane store (an S3 bucket or a directory). Other kinds return with
  issue #57. The Go module keeps all its retrievers.
- `monitoringPort`, the Swagger UI and `authorizedKeys`.
- The v1 aliases of keys such as `slackWebhookURL`: use `webhookUrl`.

## What replaced it

| Version 1 | Version 2 |
|---|---|
| Retrievers per flag set | A manifest and objects in a control plane store, written by [`semaphore-cli publish`](../tooling/publish.md) |
| Flag sets in the configuration file | Flag-set documents in the store; see [flag sets](../concepts/flagset.md) |
| Endpoint, region and credentials in each exporter | Named [connections](../concepts/connections.md) that exporters refer to |
| `monitoringPort` | An `ops` listener in `server.listeners` |
| `authorizedKeys.admin` | `auth.adminKeys`, plain strings or secret references |
| `logLevel`, `logFormat` | `observability.logging.level`, `observability.logging.format` |
| `evaluationContextEnrichment` | `evaluation.contextEnrichment` and `evaluation.environment` |
| Restart or file watch to change flags | A new generation of the manifest, applied in place |

See [getting started](../relay-proxy/getting-started.md) for a first run.

## Names that stay

These are wire contracts; the old names keep working next to the Semaphore names (see
[rename to Semaphore](rename-to-semaphore.md)):

- Evaluation context key `gofeatureflag` (and its sub-keys), next to `semaphore`.
- Response header `X-GOFEATUREFLAG-VERSION`, next to `X-SEMAPHORE-VERSION`.
- OFREP metadata `gofeatureflag_cacheable`, next to `semaphore_cacheable`.
- Metric prefix `gofeatureflag_`, only with `observability.metrics.legacyNames: true`; the default prefix is `semaphore_`.
- HTTP paths, JSON field names and the flag file format.
