# Log

## Overview
Export evaluation data inside the application logger.

## Configure the relay proxy
> [!NOTE]
> This exporter is set up by default in the relay-proxy.

## Configure the GO Module
To configure your GO module to use the Log exporter, you need to add the following
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
| `LogFormat` | No | `[{{ .FormattedDate}}] user="{{ .UserKey}}", flag="{{ .Key}}", value="{{ .Value}}"` | <p>LogFormat is the [template](https://golang.org/pkg/text/template/) configuration of the output format of your log.</p><p>You can use all the key from the `exporter.FeatureEvent` + a key called `FormattedDate` that represents the date with the **RFC 3339** Format.</p> |
