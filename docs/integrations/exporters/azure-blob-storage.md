# Azure Blob Storage

## Overview

Export evaluation data to Azure Blob Storage.

Every time `FlushInterval` or `MaxEventInMemory` is reached, a new file is uploaded to Azure Blob Storage.

> [!NOTE]
> If an Azure Blob Storage upload fails, events remain in memory and are retried on the next `FlushInterval` or `MaxEventInMemory` flush.

## Configure the relay proxy

Add the following configuration to your relay proxy configuration file:

```yaml
# ...
exporters:
  - kind: azureBlobStorage
    container: test-semaphore
    accountName: semaphore
# ...
```

| Field name                | Mandatory | Type   | Default                                                                                                                            | Description                                                                                                                                                                                    |
|---------------------------|:---------:|--------|------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `kind`                    | Yes       | string | **none**                                                                                                                           | Value should be `azureBlobStorage`.<br/>_This field is mandatory and describes which exporter you are using._                                                                                  |
| `container`               | Yes       | string | **none**                                                                                                                           | Name of your Azure Blob Storage container (for example, `my-featureflag-container`).                                                                                                           |
| `accountName`             | Yes       | string | **none**                                                                                                                           | Name of your Azure Blob Storage account.                                                                                                                                                       |
| `accountKey`              | No        | string | **none**                                                                                                                           | Secret key of your Azure Blob Storage account.                                                                                                                                                 |
| `flushInterval`           | No        | int    | `60000`                                                                                                                            | Interval in milliseconds before flushing events (flushes earlier if `maxEventInMemory` is reached first).                                                                                      |
| `maxEventInMemory`        | No        | int    | `100000`                                                                                                                           | Maximum number of events buffered in memory before flushing.                                                                                                                                   |
| `format`                  | No        | string | `JSON`                                                                                                                             | Output file format: `JSON`, `CSV`, or `Parquet`.                                                                                                                                               |
| `filename`                | No        | string | `flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}`                                                                       | Go template for exported filenames. Available fields: `{{ .Hostname}}`, `{{ .Timestamp}}`, and `{{ .Format}}`.                                                                                 |
| `csvTemplate`             | No        | string | `{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};{{ .Default}};{{ .Source}}\n` | Go template for CSV rows (ignored for other formats). See [`exporter/feature_event.go`](https://github.com/BlackbirdWorks/semaphore/blob/main/exporter/feature_event.go) for available fields. |
| `path`                    | No        | string | **container root level**                                                                                                           | Directory path inside the Azure Blob Storage container.                                                                                                                                        |
| `parquetCompressionCodec` | No        | string | `SNAPPY`                                                                                                                           | Parquet compression codec. See [available options](https://github.com/apache/parquet-format/blob/master/Compression.md).                                                                       |

## Configure the Go module

Add the following configuration to your `ffclient.Config{}` object:

```go
ffConfig := ffclient.Config{
    // ...
    DataExporter: ffclient.DataExporter{
        // ...
        Exporter: &azureexporter.Exporter{
            Container:   "test-semaphore",
            Format:      "json",
            Path:        "yourPath",
            Filename:    "flag-variation-{{ .Timestamp}}.{{ .Format}}",
            AccountName: "semaphore",
            AccountKey:  "XXXX",
        },
    },
    // ...
}
client, err := ffclient.New(ffConfig)
defer client.Close()
```

| Field name                | Mandatory | Default                                                                                                                            | Description                                                                                                                                                                                    |
|---------------------------|:---------:|------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Container`               | Yes       | **none**                                                                                                                           | Name of your Azure Blob Storage container.                                                                                                                                                     |
| `AccountName`             | Yes       | **none**                                                                                                                           | Name of your Azure Blob Storage account.                                                                                                                                                       |
| `AccountKey`              | No        | **none**                                                                                                                           | Secret key of your Azure Blob Storage account.                                                                                                                                                 |
| `Format`                  | No        | `JSON`                                                                                                                             | Output file format: `JSON`, `CSV`, or `Parquet`.                                                                                                                                               |
| `Filename`                | No        | `flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}`                                                                       | Go template for exported filenames. Available fields: `{{ .Hostname}}`, `{{ .Timestamp}}`, and `{{ .Format}}`.                                                                                 |
| `CsvTemplate`             | No        | `{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};{{ .Default}};{{ .Source}}\n` | Go template for CSV rows (ignored for other formats). See [`exporter/feature_event.go`](https://github.com/BlackbirdWorks/semaphore/blob/main/exporter/feature_event.go) for available fields. |
| `Path`                    | No        | **container root level**                                                                                                           | Directory path inside the Azure Blob Storage container.                                                                                                                                        |
| `ParquetCompressionCodec` | No        | `SNAPPY`                                                                                                                           | Parquet compression codec. See [available options](https://github.com/apache/parquet-format/blob/master/Compression.md).                                                                       |

`FlushInterval` and `MaxEventInMemory` are fields on `ffclient.DataExporter` (alongside `Exporter`), not on `azureexporter.Exporter`:

| Field              | Mandatory | Default    | Description                                                                                                 |
|--------------------|:---------:|------------|-------------------------------------------------------------------------------------------------------------|
| `FlushInterval`    | No        | `1 minute` | Interval (`time.Duration`) before flushing events (flushes earlier if `MaxEventInMemory` is reached first). |
| `MaxEventInMemory` | No        | `100000`   | Maximum number of events buffered in memory before flushing.                                                |
