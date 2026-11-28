# AWS Redshift

## Overview

The AWS Redshift exporter inserts feature flag evaluation and tracking events directly into Redshift tables using the Redshift Data API.

> [!NOTE]
> AWS Redshift is a bulk exporter. Events are collected in memory and inserted into Redshift when `FlushInterval` or `MaxEventInMemory` is reached.

## Configure the relay proxy

Add the exporter to the `exporters` map of the [bootstrap file](../../relay-proxy/bootstrap-config.md):

```yaml
version: 2
connections:
  warehouse:
    type: redshift
    region: eu-west-1
exporters:
  evaluations:
    kind: redshift
    connection: warehouse
    database: analytics
    workgroupName: analytics-serverless
    tableName: feature_flag_evaluations
    autoMigrate: true
```

- `connection` (required): a `redshift` connection.
- `database` (required) and exactly one of `clusterIdentifier` and `workgroupName`.
- Optional: `tableName`, `autoMigrate`, `secretArn`, and `dbUser` (not with `workgroupName` or `secretArn`).

An exporter delivers events of one `eventType`. The relay serves `feature` events; an exporter with another `eventType` fails to build.

The settings that every exporter takes (`flushInterval`, `maxEventInMemory`, `eventType`, `meta`, `overflowPolicy`, `retry` and the other pipeline settings) are in the [bootstrap file reference](../../relay-proxy/bootstrap-config.md) (section `exporters.<name>`).

## Configure the Go module

Add the following configuration to your `ffclient.Config{}` object:

```go
awsConfig, _ := config.LoadDefaultConfig(context.Background())

ffConfig := ffclient.Config{
    // ...
    DataExporters: []ffclient.DataExporter{
        {
            ExporterEventType: ffclient.FeatureEventExporter,
            Exporter: &redshiftexporter.Exporter{
                ClusterIdentifier: "my-cluster",
                Database:          "analytics",
                DBUser:            "semaphore_writer",
                TableName:         "feature_flag_evaluations",
                AwsConfig:         &awsConfig,
                AutoMigrate:       true,
            },
        },
        {
            ExporterEventType: ffclient.TrackingEventExporter,
            Exporter: &redshiftexporter.Exporter{
                ClusterIdentifier: "my-cluster",
                Database:          "analytics",
                DBUser:            "semaphore_writer",
                TableName:         "tracking_events",
                AwsConfig:         &awsConfig,
                AutoMigrate:       true,
            },
        },
    },
    // ...
}
client, err := ffclient.New(ffConfig)
defer client.Close()
```

| Field               | Mandatory | Default                                                            | Description                                                                                                                |
|---------------------|:---------:|--------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------|
| `Database`          | Yes       | **none**                                                           | Name of the Redshift database receiving the events.                                                                        |
| `ClusterIdentifier` | No        | **none**                                                           | Identifier of a provisioned Redshift cluster. Set either `ClusterIdentifier` or `WorkgroupName`.                           |
| `WorkgroupName`     | No        | **none**                                                           | Name or ARN of a Redshift Serverless workgroup. Set either `ClusterIdentifier` or `WorkgroupName`.                         |
| `DBUser`            | No        | **none**                                                           | Database user used with `ClusterIdentifier` for temporary credentials. Cannot be used with `WorkgroupName` or `SecretArn`. |
| `SecretArn`         | No        | **none**                                                           | ARN of an AWS Secrets Manager secret containing database credentials. Cannot be used with `DBUser`.                        |
| `TableName`         | No        | `feature_flag_evaluations` (feature), `tracking_events` (tracking) | Target table name. Can be schema-qualified (`my_schema.my_table`).                                                         |
| `AwsConfig`         | No        | **none**                                                           | AWS SDK configuration. If empty, the default AWS configuration is loaded.                                                  |
| `AutoMigrate`       | No        | `false`                                                            | When `true`, creates the target table if it does not exist.                                                                |

`FlushInterval` and `MaxEventInMemory` are fields on `ffclient.DataExporter` (alongside `Exporter`), not on `redshiftexporter.Exporter`:

| Field              | Mandatory | Default    | Description                                                                                                     |
|--------------------|:---------:|------------|-----------------------------------------------------------------------------------------------------------------|
| `FlushInterval`    | No        | `1 minute` | Interval (`time.Duration`) before flushing events (flushes earlier if `MaxEventInMemory` is reached first).     |
| `MaxEventInMemory` | No        | `100000`   | Maximum number of events buffered in memory before flushing.                                                    |

## Authentication

The exporter uses the [Redshift Data API](https://docs.aws.amazon.com/redshift/latest/mgmt/data-api.html), so no JDBC connection or database password is needed. Depending on the fields set, it authenticates with:

| Authentication method                       | Fields                                                                   | Required permission                                                                                                                                                |
|---------------------------------------------|--------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **IAM identity**                            | `clusterIdentifier` (or `workgroupName`) and `database`                  | `redshift:GetClusterCredentialsWithIAM` for a provisioned cluster; `redshift-serverless:GetCredentials` for Serverless. The database user is derived from the IAM identity. |
| **Temporary credentials for a database user** | `clusterIdentifier`, `database`, and `dbUser` (provisioned clusters only) | `redshift:GetClusterCredentials`                                                                                                                                   |
| **AWS Secrets Manager**                     | `clusterIdentifier` (or `workgroupName`), `database`, and `secretArn`    | `secretsmanager:GetSecretValue` on the secret                                                                                                                      |

The IAM identity used by Semaphore also needs these Redshift Data API permissions:

- `redshift-data:BatchExecuteStatement`: run `INSERT` and `CREATE TABLE` statements.
- `redshift-data:DescribeStatement`: wait for statement completion. **Required**: without it, `INSERT` still runs, but the exporter cannot confirm success and retries on the next flush.
- `redshift-data:CancelStatement`: cancel transactions that exceed the timeout.

## How events are inserted

- Each flush uses multi-row `INSERT` statements with named parameters, split to stay under Redshift Data API limits (at most 500 rows and ~100KB per statement).
- Statements run via `BatchExecuteStatement` in a **single transaction** (up to 40 statements and ~1MB). If any statement fails, that transaction rolls back and events are retried on the next flush.
- Larger flushes split across multiple transactions; if a later transaction fails, retrying re-inserts already committed rows (at-least-once delivery).
- Because the Redshift Data API is asynchronous, the exporter waits up to 5 minutes per transaction before cancelling and retrying on the next flush.

### Values that do not fit in the table

- Text values exceeding their column byte limit are truncated without splitting multibyte characters.
- Events with a JSON value (`value`, `metadata`, `tracking_details`, `evaluation_context`) larger than 65,535 bytes, or too large for a single statement, are skipped and logged as a warning with the skipped count and flag keys.

## AutoMigrate

When `autoMigrate` / `AutoMigrate` is `true`, the exporter runs `CREATE TABLE IF NOT EXISTS` once per exporter instance using the schemas below.

> [!WARNING]
> AutoMigrate only creates the **table**, not the schema. If you use a schema-qualified `tableName` (e.g. `my_schema.my_table`), create the schema first (`CREATE SCHEMA IF NOT EXISTS my_schema`), otherwise `CREATE TABLE` fails.

## Table schemas

### `feature_flag_evaluations`

| Column          | Redshift type | Source field   |
|-----------------|---------------|----------------|
| `kind`          | VARCHAR(50)   | `Kind`         |
| `context_kind`  | VARCHAR(50)   | `ContextKind`  |
| `user_key`      | VARCHAR(512)  | `UserKey`      |
| `creation_date` | BIGINT        | `CreationDate` |
| `key`           | VARCHAR(256)  | `Key`          |
| `variation`     | VARCHAR(256)  | `Variation`    |
| `value`         | SUPER         | `Value`        |
| `default`       | BOOLEAN       | `Default`      |
| `version`       | VARCHAR(64)   | `Version`      |
| `source`        | VARCHAR(64)   | `Source`       |
| `metadata`      | SUPER         | `Metadata`     |

### `tracking_events`

| Column               | Redshift type | Source field        |
|----------------------|---------------|---------------------|
| `kind`               | VARCHAR(50)   | `Kind`              |
| `context_kind`       | VARCHAR(50)   | `ContextKind`       |
| `user_key`           | VARCHAR(512)  | `UserKey`           |
| `creation_date`      | BIGINT        | `CreationDate`      |
| `key`                | VARCHAR(256)  | `Key`               |
| `tracking_details`   | SUPER         | `TrackingDetails`   |
| `evaluation_context` | SUPER         | `EvaluationContext` |

`SUPER` columns are inserted with `JSON_PARSE`.
