# Log

## Overview

Export evaluation data to the application logger.

## Configure the relay proxy

> [!NOTE]
> This exporter is enabled by default in the relay proxy.

## Configure the Go module

Add the following configuration to your `ffclient.Config{}` object:

```go
ffConfig := ffclient.Config{
    // ...
    DataExporter: ffclient.DataExporter{
        Exporter: &logsexporter.Exporter{
            LogFormat: "[{{ .FormattedDate}}] user=\"{{ .UserKey}}\", flag=\"{{ .Key}}\", value=\"{{ .Value}}\"",
        },
    },
    // ...
}
client, err := ffclient.New(ffConfig)
defer client.Close()
```

| Field name  | Mandatory | Default                                                                             | Description                                                                                                                                                                 |
|-------------|:---------:|-------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `LogFormat` | No        | `[{{ .FormattedDate}}] user="{{ .UserKey}}", flag="{{ .Key}}", value="{{ .Value}}"` | [Go template](https://pkg.go.dev/text/template) for each log line. Supports all fields from `exporter.FeatureEvent` plus `FormattedDate` (**RFC 3339** format).              |
