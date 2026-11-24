# Google Cloud Storage

## Overview
Export evaluation data to a Google Cloud Storage Bucket.

Everytime the `FlushInterval` or `MaxEventInMemory` is reached a new file will be added to Google Cloud Storage.

> [!NOTE]
> If for some reason the Google Cloud Storage upload fails, we will keep the data in memory and retry to add the next time we reach `FlushInterval` or `MaxEventInMemory`.

## Configure the relay proxy

To configure your relay proxy to use the Google Cloud Storage exporter, you need to add the following
configuration to your relay proxy configuration file:

```yaml
# ...
exporters:
  - kind: googleStorage
    bucket: test-semaphore
# ...
```

| Field name                |    Mandatory     | Type   | Default                                                                                                                            | Description                                                                                                                                                                                                                                                                                                                                                                           |
|---------------------------|:----------------:|--------|------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `kind`                    |  Yes   | string | **none**                                                                                                                           | **Value should be `googleStorage`**.<br/>_This field is mandatory and describes which retriever you are using._                                                                                                                                                                                                                                                                       |
| `bucket`                  |  Yes   | string | **none**                                                                                                                           | Name of your Google Cloud Storage Bucket.                                                                                                                                                                                                                                                                                                                                             |
| `path`                    | No | string | **bucket root level**                                                                                                              | The location of the directory in Google Cloud Storage.                                                                                                                                                                                                                                                                                                                                |
| `flushInterval`           | No | int    | `60000`                                                                                                                            | The interval in millisecond between 2 calls to the webhook _(if the `maxEventInMemory` is reached before the flushInterval we will call the exporter before)_.                                                                                                                                                                                                                        |
| `maxEventInMemory`        | No | int    | `100000`                                                                                                                           | If we hit that limit we will call the exporter.                                                                                                                                                                                                                                                                                                                                       |
| `format`                  | No | string | `JSON`                                                                                                                             | Format is the output format you want in your exported file. Available format: `JSON`, `CSV`, `Parquet`.                                                                                                                                                                                                                                                                               |
| `filename`                | No | string | `flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}`                                                                       | You can use a config template to define the name of your exported files. Available replacements are `{{ .Hostname}}`, `{{ .Timestamp}}` and `{{ .Format}`                                                                                                                                                                                                                             |
| `csvTemplate`             | No | string | `{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};{{ .Default}};{{ .Source}}\n` | CsvTemplate is used if your output format is CSV.<br/>This field will be ignored if you are using format other than CSV.<br/>You can decide which fields you want in your CSV line with a go-template syntax, please check [`exporter/feature_event.go`](https://github.com/BlackbirdWorks/semaphore/blob/main/exporter/feature_event.go) to see what are the fields available. |
| `parquetCompressionCodec` | No | string | `SNAPPY`                                                                                                                           | ParquetCompressionCodec is the parquet compression codec for better space efficiency. [Available options](https://github.com/apache/parquet-format/blob/master/Compression.md)                                                                                                                                                                                                        |

## Configure the GO Module
To configure your GO module to use the Google Cloud Storage exporter, you need to add the following
configuration to your `ffclient.Config{}` object:

```go
config := ffclient.Config{
    // ...
   DataExporter: ffclient.DataExporter{
        // ...
        Exporter: &gcstorageexporter.Exporter{
            Bucket:   "test-semaphore",
            Format:   "json",
            Path:     "yourPath",
            Filename: "flag-variation-{{ .Timestamp}}.{{ .Format}}",
            Options:  []option.ClientOption{}, // Your google cloud SDK options
        },
    },
    // ...
}
client, err := ffclient.New(config)
defer client.Close()
```

| Field name                |    Mandatory     | Default                                                                                                                            | Description                                                                                                                                                                                                                                                                                                                                                                           |
|---------------------------|:----------------:|------------------------------------------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Bucket `                 |  Yes   | none                                                                                                                               | Name of your Google Cloud Storage Bucket.                                                                                                                                                                                                                                                                                                                                             |
| `Options`                 | No | none                                                                                                                               | An instance of `option.ClientOption` that configures your access to Google Cloud. <br/> Check [this documentation for more info](https://cloud.google.com/docs/authentication).                                                                                                                                                                                                       |
| `FlushInterval`           | No | `60000`                                                                                                                            | The interval in millisecond between 2 calls to the webhook _(if the `maxEventInMemory` is reached before the flushInterval we will call the exporter before)_.                                                                                                                                                                                                                        |
| `MaxEventInMemory`        | No | `100000`                                                                                                                           | If we hit that limit we will call the exporter.                                                                                                                                                                                                                                                                                                                                       |
| `Format`                  | No | `JSON`                                                                                                                             | Format is the output format you want in your exported file. Available format: `JSON`, `CSV`, `Parquet`.                                                                                                                                                                                                                                                                               |
| `Filename`                | No | `flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}`                                                                       | You can use a config template to define the name of your exported files. Available replacements are `{{ .Hostname}}`, `{{ .Timestamp}}` and `{{ .Format}`                                                                                                                                                                                                                             |
| `CsvTemplate`             | No | `{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};{{ .Default}};{{ .Source}}\n` | CsvTemplate is used if your output format is CSV.<br/>This field will be ignored if you are using format other than CSV.<br/>You can decide which fields you want in your CSV line with a go-template syntax, please check [`exporter/feature_event.go`](https://github.com/BlackbirdWorks/semaphore/blob/main/exporter/feature_event.go) to see what are the fields available. |`
| `Path`                    | No | **bucket root level**                                                                                                              | The location of the directory in S3.                                                                                                                                                                                                                                                                                                                                                  |
| `ParquetCompressionCodec` | No | `SNAPPY`                                                                                                                           | ParquetCompressionCodec is the parquet compression codec for better space efficiency. [Available options](https://github.com/apache/parquet-format/blob/master/Compression.md)                                                                                                                                                                                                        |`
