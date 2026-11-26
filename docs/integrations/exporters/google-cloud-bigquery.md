# Google Cloud BigQuery

## Overview

The Google Cloud BigQuery exporter streams feature flag evaluation and tracking events directly to BigQuery using the official streaming-insert API.

> [!NOTE]
> Google Cloud BigQuery is a bulk exporter. Events are collected in memory and streamed to BigQuery when `FlushInterval` or `MaxEventInMemory` is reached.

## Configure the relay proxy

Add the following configuration to your relay proxy configuration file:

```yaml
# ...
exporters:
  - kind: bigquery
    projectID: "my-project-id"
    datasetID: "semaphore_events"
    tableName: "feature_flag_evaluations"
    googleCredentials: |
      {"type":"service_account","project_id":"my-project-id"}
    autoMigrate: true
    eventType: "feature"
# ...
```

> [!NOTE]
> Each relay proxy exporter targets a single `eventType` (defaulting to `feature`). To export **both** evaluation and tracking events, declare two BigQuery exporters:
>
> ```yaml
> # ...
> exporters:
>   - kind: bigquery
>     projectID: "my-project-id"
>     datasetID: "semaphore_events"
>     tableName: "feature_flag_evaluations"
>     autoMigrate: true
>     eventType: "feature"
>   - kind: bigquery
>     projectID: "my-project-id"
>     datasetID: "semaphore_events"
>     tableName: "tracking_events"
>     autoMigrate: true
>     eventType: "tracking"
> # ...
> ```

| Field name          | Mandatory | Type    | Default                    | Description                                                                                               |
|---------------------|:---------:|---------|----------------------------|-----------------------------------------------------------------------------------------------------------|
| `kind`              | Yes       | string  | **none**                   | Value should be `bigquery`.<br/>_This field is mandatory and describes which exporter you are using._       |
| `projectID`         | Yes       | string  | **none**                   | ID of the GCP project containing the BigQuery dataset.                                                    |
| `datasetID`         | Yes       | string  | **none**                   | BigQuery dataset where events are inserted.                                                               |
| `tableName`         | No        | string  | `feature_flag_evaluations` | BigQuery table used by this exporter instance. Set to your tracking table for `eventType: "tracking"`.    |
| `googleCredentials` | No        | string  | **none**                   | Google credentials JSON. If empty, Application Default Credentials are used.                              |
| `autoMigrate`       | No        | boolean | `false`                    | When `true`, creates the dataset and target table if they do not exist.                                   |
| `eventType`         | No        | string  | `feature`                  | Event type to export: `feature` for evaluation events or `tracking` for tracking events.                  |

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
