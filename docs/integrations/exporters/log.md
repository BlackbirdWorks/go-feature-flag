# Log

## Overview

Export evaluation data to the application logger.

## Configure the relay proxy

Add the exporter to the `exporters` map of the [bootstrap file](../../relay-proxy/bootstrap-config.md):

```yaml
version: 2
exporters:
  evaluations:
    kind: log
    logFormat: '[{{ .FormattedDate}}] user="{{ .UserKey}}", flag="{{ .Key}}", value="{{ .Value}}"'
```

- Optional: `logFormat`, a Go template of one line (the default is the one shown).

The exporter is not enabled by default: the relay runs the exporters of the `exporters` map and no other.

The settings that every exporter takes (`flushInterval`, `maxEventInMemory`, `eventType`, `meta`, `overflowPolicy`, `retry` and the other pipeline settings) are in the [bootstrap file reference](../../relay-proxy/bootstrap-config.md) (section `exporters.<name>`).

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
