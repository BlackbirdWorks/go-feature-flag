# File System

## Overview

Export evaluation data to a directory on your local file system.

Every time `FlushInterval` or `MaxEventInMemory` is reached, a new file is written to the output directory.

> [!NOTE]
> If writing the file fails, events remain in memory and are retried on the next `FlushInterval` or `MaxEventInMemory` flush.

## Configure the relay proxy

Add the exporter to the `exporters` map of the [bootstrap file](../../relay-proxy/bootstrap-config.md):

```yaml
version: 2
exporters:
  evaluations:
    kind: file
    outputDir: /var/semaphore/exports
    format: json
```

- `outputDir` (required): the directory of the files.
- Optional: `format` (`json`, `csv`, `parquet`), `filename`, `csvTemplate`, `parquetCompressionCodec`.

The settings that every exporter takes (`flushInterval`, `maxEventInMemory`, `eventType`, `meta`, `overflowPolicy`, `retry` and the other pipeline settings) are in the [bootstrap file reference](../../relay-proxy/bootstrap-config.md) (section `exporters.<name>`).

## Configure the Go module

Add the following configuration to your `ffclient.Config{}` object:

```go
ffConfig := ffclient.Config{
    // ...
    DataExporter: ffclient.DataExporter{
        // ...
        Exporter: &fileexporter.Exporter{
            OutputDir:   "/output-data/",
            Format:      "csv",
            Filename:    "flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}",
            CsvTemplate: "{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};{{ .Default}};{{ .Source}}\n",
        },
    },
    // ...
}
client, err := ffclient.New(ffConfig)
defer client.Close()
```

| Field name                | Mandatory | Default                                                                                                                            | Description                                                                                                                                                                                    |
|---------------------------|:---------:|------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `OutputDir`               | Yes       | **none**                                                                                                                           | Output directory where exported files are stored.                                                                                                                                              |
| `Format`                  | No        | `JSON`                                                                                                                             | Output file format: `JSON`, `CSV`, or `Parquet`.                                                                                                                                               |
| `Filename`                | No        | `flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}`                                                                       | Go template for exported filenames. Available fields: `{{ .Hostname}}`, `{{ .Timestamp}}`, and `{{ .Format}}`.                                                                                 |
| `CsvTemplate`             | No        | `{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};{{ .Default}};{{ .Source}}\n` | Go template for CSV rows (ignored for other formats). See [`exporter/feature_event.go`](https://github.com/BlackbirdWorks/semaphore/blob/main/exporter/feature_event.go) for available fields. |
| `ParquetCompressionCodec` | No        | `SNAPPY`                                                                                                                           | Parquet compression codec. See [available options](https://github.com/apache/parquet-format/blob/master/Compression.md).                                                                       |

`FlushInterval` and `MaxEventInMemory` are fields on `ffclient.DataExporter` (alongside `Exporter`), not on `fileexporter.Exporter`:

| Field              | Mandatory | Default    | Description                                                                                                 |
|--------------------|:---------:|------------|-------------------------------------------------------------------------------------------------------------|
| `FlushInterval`    | No        | `1 minute` | Interval (`time.Duration`) before flushing events (flushes earlier if `MaxEventInMemory` is reached first). |
| `MaxEventInMemory` | No        | `100000`   | Maximum number of events buffered in memory before flushing.                                                |
