# Google Cloud Storage

## Overview

Export evaluation data to a Google Cloud Storage bucket.

Every time `FlushInterval` or `MaxEventInMemory` is reached, a new file is uploaded to Google Cloud Storage.

> [!NOTE]
> If a Google Cloud Storage upload fails, events remain in memory and are retried on the next `FlushInterval` or `MaxEventInMemory` flush.

## Configure the relay proxy

Add the exporter to the `exporters` map of the [bootstrap file](../../relay-proxy/bootstrap-config.md):

```yaml
version: 2
exporters:
  evaluations:
    kind: googleStorage
    bucket: evaluation-data-bucket
    path: evaluations/
```

- `bucket` (required).
- Optional: `googleCredentials` (a secret), `path`, `format` (`json`, `csv`, `parquet`), `filename`, `csvTemplate`, `parquetCompressionCodec`.

The settings that every exporter takes (`flushInterval`, `maxEventInMemory`, `eventType`, `meta`, `overflowPolicy`, `retry` and the other pipeline settings) are in the [bootstrap file reference](../../relay-proxy/bootstrap-config.md) (section `exporters.<name>`).

## Configure the Go module

Add the following configuration to your `ffclient.Config{}` object:

```go
ffConfig := ffclient.Config{
    // ...
    DataExporter: ffclient.DataExporter{
        // ...
        Exporter: &gcstorageexporter.Exporter{
            Bucket:   "test-semaphore",
            Format:   "json",
            Path:     "yourPath",
            Filename: "flag-variation-{{ .Timestamp}}.{{ .Format}}",
            Options:  []option.ClientOption{}, // your Google Cloud SDK options
        },
    },
    // ...
}
client, err := ffclient.New(ffConfig)
defer client.Close()
```

| Field name                | Mandatory | Default                                                                                                                            | Description                                                                                                                                                                                    |
|---------------------------|:---------:|------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Bucket`                  | Yes       | **none**                                                                                                                           | Name of your Google Cloud Storage bucket.                                                                                                                                                      |
| `Options`                 | No        | **none**                                                                                                                           | An instance of `[]option.ClientOption` configuring Google Cloud access (see [Google Cloud auth docs](https://cloud.google.com/docs/authentication)).                                          |
| `Format`                  | No        | `JSON`                                                                                                                             | Output file format: `JSON`, `CSV`, or `Parquet`.                                                                                                                                               |
| `Filename`                | No        | `flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}`                                                                       | Go template for exported filenames. Available fields: `{{ .Hostname}}`, `{{ .Timestamp}}`, and `{{ .Format}}`.                                                                                 |
| `CsvTemplate`             | No        | `{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};{{ .Default}};{{ .Source}}\n` | Go template for CSV rows (ignored for other formats). See [`exporter/feature_event.go`](https://github.com/BlackbirdWorks/semaphore/blob/main/exporter/feature_event.go) for available fields. |
| `Path`                    | No        | **bucket root level**                                                                                                              | Directory path inside the Google Cloud Storage bucket.                                                                                                                                         |
| `ParquetCompressionCodec` | No        | `SNAPPY`                                                                                                                           | Parquet compression codec. See [available options](https://github.com/apache/parquet-format/blob/master/Compression.md).                                                                       |

`FlushInterval` and `MaxEventInMemory` are fields on `ffclient.DataExporter` (alongside `Exporter`), not on `gcstorageexporter.Exporter`:

| Field              | Mandatory | Default    | Description                                                                                                 |
|--------------------|:---------:|------------|-------------------------------------------------------------------------------------------------------------|
| `FlushInterval`    | No        | `1 minute` | Interval (`time.Duration`) before flushing events (flushes earlier if `MaxEventInMemory` is reached first). |
| `MaxEventInMemory` | No        | `100000`   | Maximum number of events buffered in memory before flushing.                                                |
