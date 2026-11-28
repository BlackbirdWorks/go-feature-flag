# Google Cloud BigQuery

## Overview

The Google Cloud BigQuery exporter streams feature flag evaluation and tracking events directly to BigQuery using the official streaming-insert API.

> [!NOTE]
> Google Cloud BigQuery is a bulk exporter. Events are collected in memory and streamed to BigQuery when `FlushInterval` or `MaxEventInMemory` is reached.

## Configure the relay proxy

Add the exporter to the `exporters` map of the [bootstrap file](../../relay-proxy/bootstrap-config.md):

```yaml
version: 2
exporters:
  evaluations:
    kind: bigquery
    projectId: my-project
    datasetId: semaphore
    tableName: feature_flag_evaluations
    autoMigrate: true
```

- `projectId` and `datasetId` (required).
- Optional: `tableName`, `autoMigrate`, `googleCredentials` (a secret: `{fromEnv: NAME}` or `{fromFile: PATH}`).

An exporter delivers events of one `eventType`. The relay serves `feature` events; an exporter with another `eventType` fails to build.

The settings that every exporter takes (`flushInterval`, `maxEventInMemory`, `eventType`, `meta`, `overflowPolicy`, `retry` and the other pipeline settings) are in the [bootstrap file reference](../../relay-proxy/bootstrap-config.md) (section `exporters.<name>`).

## Configure the Go module

Add the following configuration to your `ffclient.Config{}` object:

```go
googleCredentials := []byte(`{...}`)

ffConfig := ffclient.Config{
    // ...
    DataExporters: []ffclient.DataExporter{
        {
            ExporterEventType: ffclient.FeatureEventExporter,
            Exporter: &bigqueryexporter.Exporter{
                ProjectID:         "my-project-id",
                DatasetID:         "semaphore_events",
                TableName:         "feature_flag_evaluations",
                AutoMigrate:       true,
                GoogleCredentials: googleCredentials,
            },
        },
        {
            ExporterEventType: ffclient.TrackingEventExporter,
            Exporter: &bigqueryexporter.Exporter{
                ProjectID:         "my-project-id",
                DatasetID:         "semaphore_events",
                TableName:         "tracking_events",
                AutoMigrate:       true,
                GoogleCredentials: googleCredentials,
            },
        },
    },
    // ...
}
client, err := ffclient.New(ffConfig)
defer client.Close()
```

| Field               | Mandatory | Default                    | Description                                                                |
|---------------------|:---------:|----------------------------|----------------------------------------------------------------------------|
| `ProjectID`         | Yes       | **none**                   | ID of the GCP project containing the BigQuery dataset.                     |
| `DatasetID`         | Yes       | **none**                   | BigQuery dataset where events are inserted.                                |
| `TableName`         | No        | `feature_flag_evaluations` | BigQuery table used by this exporter instance.                             |
| `AutoMigrate`       | No        | `false`                    | When `true`, creates the dataset and target table if they do not exist.    |
| `GoogleCredentials` | No        | **none**                   | Google credentials JSON. If empty, Application Default Credentials are used.|

## Authentication

- When `GoogleCredentials` / `googleCredentials` is empty, the exporter uses [Application Default Credentials](https://cloud.google.com/docs/authentication/application-default-credentials).
- Provide explicit credentials JSON in `googleCredentials` (relay proxy) or `GoogleCredentials` (Go module) when ADC is unavailable.

## AutoMigrate

- When `autoMigrate` / `AutoMigrate` is `true`, the exporter creates the configured dataset and target table if missing.
- Existing datasets and tables are treated as success.
- Only the table configured for that exporter instance is checked (for example, a tracking exporter with `tableName: "tracking_events"` ensures `tracking_events`).

## Table schemas

### `feature_flag_evaluations`

| Column          | BigQuery type | Source field   |
|-----------------|---------------|----------------|
| `kind`          | STRING        | `Kind`         |
| `context_kind`  | STRING        | `ContextKind`  |
| `user_key`      | STRING        | `UserKey`      |
| `creation_date` | INTEGER       | `CreationDate` |
| `key`           | STRING        | `Key`          |
| `variation`     | STRING        | `Variation`    |
| `value`         | JSON          | `Value`        |
| `default`       | BOOL          | `Default`      |
| `version`       | STRING        | `Version`      |
| `source`        | STRING        | `Source`       |
| `metadata`      | JSON          | `Metadata`     |

### `tracking_events`

| Column               | BigQuery type | Source field        |
|----------------------|---------------|---------------------|
| `kind`               | STRING        | `Kind`              |
| `context_kind`       | STRING        | `ContextKind`       |
| `user_key`           | STRING        | `UserKey`           |
| `creation_date`      | INTEGER       | `CreationDate`      |
| `key`                | STRING        | `Key`               |
| `tracking_details`   | JSON          | `TrackingDetails`   |
| `evaluation_context` | JSON          | `EvaluationContext` |

`JSON` columns are inserted as JSON-marshaled strings.
