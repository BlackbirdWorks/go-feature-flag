# Log

## Overview
Export evaluation data inside the application logger.

## Configure the relay proxy
> [!NOTE]
> This exporter is set up by default in the relay-proxy.

## Configure the Go Module
To configure your Go module to use the Log exporter, you need to add the following
configuration to your `ffclient.Config{}` object:

```go
config := ffclient.Config{
    // ...
   DataExporter: ffclient.DataExporter{
        Exporter: &logsexporter.Exporter{
            LogFormat: "[{{ .FormattedDate}}] user=\"{{ .UserKey}}\", flag=\"{{ .Key}}\", value=\"{{ .Value}}\"",
        },
    },
    // ...
}
client, err := ffclient.New(config)
defer client.Close()
```

| Field name  |    Mandatory     | Default                                                                             | Description                                                                                                                                                                                                                                                                    |
|-------------|:----------------:|-------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `LogFormat` | No | `[{{ .FormattedDate}}] user="{{ .UserKey}}", flag="{{ .Key}}", value="{{ .Value}}"` | LogFormat is the [template](https://golang.org/pkg/text/template/) configuration of the output format of your log.<br/>You can use all keys from `exporter.FeatureEvent` plus `FormattedDate` (**RFC 3339** format). |
