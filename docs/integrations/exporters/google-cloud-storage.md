# Google Cloud Storage

## Overview

Export evaluation data to a Google Cloud Storage bucket.

Every time `FlushInterval` or `MaxEventInMemory` is reached, a new file is uploaded to Google Cloud Storage.

> [!NOTE]
> If a Google Cloud Storage upload fails, events remain in memory and are retried on the next `FlushInterval` or `MaxEventInMemory` flush.

## Configure the relay proxy

Add the following configuration to your relay proxy configuration file:

```yaml
# ...
exporters:
  - kind: googleStorage
    bucket: test-semaphore
# ...
```

| Field name                | Mandatory | Type   | Default                                                                                                                            | Description                                                                                                                                                                                    |
|---------------------------|:---------:|--------|------------------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `kind`                    | Yes       | string | **none**                                                                                                                           | Value should be `googleStorage`.<br/>_This field is mandatory and describes which exporter you are using._                                                                                     |
| `bucket`                  | Yes       | string | **none**                                                                                                                           | Name of your Google Cloud Storage bucket.                                                                                                                                                      |
| `path`                    | No        | string | **bucket root level**                                                                                                              | Directory path inside the Google Cloud Storage bucket.                                                                                                                                         |
| `flushInterval`           | No        | int    | `60000`                                                                                                                            | Interval in milliseconds before flushing events (flushes earlier if `maxEventInMemory` is reached first).                                                                                      |
| `maxEventInMemory`        | No        | int    | `100000`                                                                                                                           | Maximum number of events buffered in memory before flushing.                                                                                                                                   |
| `format`                  | No        | string | `JSON`                                                                                                                             | Output file format: `JSON`, `CSV`, or `Parquet`.                                                                                                                                               |
| `filename`                | No        | string | `flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}`                                                                       | Go template for exported filenames. Available fields: `{{ .Hostname}}`, `{{ .Timestamp}}`, and `{{ .Format}}`.                                                                                 |
| `csvTemplate`             | No        | string | `{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};{{ .Default}};{{ .Source}}\n` | Go template for CSV rows (ignored for other formats). See [`exporter/feature_event.go`](https://github.com/BlackbirdWorks/semaphore/blob/main/exporter/feature_event.go) for available fields. |
| `parquetCompressionCodec` | No        | string | `SNAPPY`                                                                                                                           | Parquet compression codec. See [available options](https://github.com/apache/parquet-format/blob/master/Compression.md).                                                                       |

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
