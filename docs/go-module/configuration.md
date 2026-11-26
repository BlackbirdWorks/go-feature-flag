# Configuration

Initialize `semaphore` by passing an [`ffclient.Config`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#Config) struct to `ffclient.New` (uses `context.Background()`) or `ffclient.NewWithContext(ctx, cfg)` (passes a custom context for retriever initialization).

## Configuration fields

| Field | Description |
|---|---|
| `Retriever` | *(optional if `Retrievers` is set)* Flag configuration retriever. See [Retrieve your feature flags configuration](store_file.md). |
| `Retrievers` | *(optional if `Retriever` is set)* List of flag retrievers. Fetched in parallel and applied in the order listed (later entries override earlier flags with the same key). See [Retrieve your feature flags configuration](store_file.md). |
| `Environment` | *(optional)* Environment the app runs in, available in feature flag rules. Default: `""`. |
| `DataExporter` | *(optional)* Single exporter for flag variation events. See [Export evaluation data](data_collection.md). |
| `DataExporters` | *(optional)* Multiple exporters for variation events, flushed in parallel without interdependencies. |
| `FileFormat` | *(optional)* Flag configuration file format (`yaml`, `toml`, or `json`). Default: `YAML`. |
| `LeveledLogger` | *(optional)* `*slog.Logger` for library logs. Default: `nil` (no logging). |
| `Notifiers` | *(optional)* List of notifiers called when flags change. See [Notify flag changes](notifier.md). |
| `PollingInterval` | *(optional)* Wait duration between flag refreshes (minimum `1 * time.Second`; negative values disable polling). Default: `60 * time.Second`. |
| `EnablePollingJitter` | *(optional)* Adds up to ±10% random deviation to `PollingInterval` to avoid thundering-herd spikes across instances. Default: `false`. |
| `StartWithRetrieverError` | *(optional)* Start without returning an error from `New` if initial retrieval fails; serves default values until flags load. Default: `false`. |
| `DisableNotifierOnInit` | *(optional)* Skip calling notifiers during initial flag load on startup. Default: `false`. |
| `Offline` | *(optional)* Disable flag retrieval, data export, and notifications; serves default values. Default: `false`. |
| `EvaluationContextEnrichment` | *(optional)* Free-form `map[string]any` merged as custom attributes on every evaluation. Overrides matching keys in the request context; if `Environment` is set, an `env` key in `EvaluationContextEnrichment` is ignored. Default: `nil`. |
| `PersistentFlagConfigurationFile` | *(optional)* Local file path to cache flag configuration as a startup fallback if retrievers are unreachable (may serve stale flags; e.g., `/tmp/semaphore_persist_conf.yaml`). Default: `""` (not persisted). |

## Example

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    LeveledLogger:   slog.Default(),
    Environment:     os.Getenv("MYAPP_ENV"),
    Retriever:       &fileretriever.Retriever{Path: "testdata/flag-config.yaml"},
    FileFormat:      "yaml",
    Notifiers: []notifier.Notifier{
        &webhooknotifier.Notifier{
            EndpointURL: "https://example.com/hook",
            Secret:      "Secret",
            Meta: map[string]string{
                "app.name": "my app",
            },
        },
    },
    DataExporter: ffclient.DataExporter{
        FlushInterval:    10 * time.Second,
        MaxEventInMemory: 1000,
        Exporter: &file.Exporter{
            OutputDir: "/output-data/",
        },
    },
    StartWithRetrieverError: false,
})
```

## Working with multiple clients

There is no package-level singleton client. Create a client with `ffclient.New` at startup, pass it where flags are evaluated, and call `Close` when your application stops.

You can create multiple [`GoFeatureFlag`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#GoFeatureFlag) clients in the same application, each with its own configuration, retrievers, and flags. Keep track of each client instance in your application code:

```go
x, err := ffclient.New(&ffclient.Config{
    Retriever: &httpretriever.Retriever{URL: "http://example.com/flag-config.yaml"},
})
if err != nil {
    return err
}
defer x.Close()

y, err := ffclient.New(&ffclient.Config{
    Retriever: &httpretriever.Retriever{URL: "http://example.com/test2.yaml"},
})
if err != nil {
    return err
}
defer y.Close()

user := ffcontext.NewEvaluationContext("user-key")
x.BoolVariation("test-flag", user, false)
y.BoolVariation("test-flag", user, false)
```

## Offline mode

Set `Offline: true` in `ffclient.Config` to stop remote retrieval, data export, and notifications and fall back to default values (useful for on-premise or disconnected environments).

## Advanced configuration

- [Export data from your flag variations](data_collection.md)
- [Be notified when your flags change](notifier.md)
