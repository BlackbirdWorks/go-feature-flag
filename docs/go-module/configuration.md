# Configuration
`semaphore` needs to be initialized to be used.  
During the initialization you must give a [`ffclient.Config{}`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#Config) configuration object.  

[`ffclient.Config{}`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#Config) is the only location where you can put the configuration.

## Configuration fields

| Field                             | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
|-----------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `Retriever`                       | The configuration retriever you want to use to get your flag file<br/> *See [Store your flag file](store_file.md) for the configuration details*.<br /><br /> *This field is optional if `Retrievers`* is configured.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `Retrievers`                      | *(optional)*<br/>`Retrievers` is exactly the same thing as `Retriever` but you can configure more than 1 source for your flags.<br/>All flags are retrieved in parallel, but we are applying them in the order you provided them _(it means that a flag can be overridden by another flag)_. <br/>*See [Store your flag file](store_file.md) for the configuration details*. <br /><br /> *This field is optional if `Retrievers`* is configured.                                                                                                                                                                                                                                                      |
| `Context`                         | *(optional)*<br/>The context used by the retriever.<br />Default: **`context.Background()`**                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| `Environment`                     | <a name="option_environment"></a>*(optional)*<br/>The environment the app is running under, can be checked in feature flag rules.<br />Default: `""`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `DataExporter`                    | *(optional)*<br/>`DataExporter` defines the method for exporting data on the usage of your flags.<br/> *see [export data section](data_collection.md) for more details*.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| `DataExporters`                   | *(optional)*<br/>`DataExporters` is exactly the same thing as `DataExporter` but you can configure more than 1 exporter for your variation events.<br/>All exporters are flushed in parallel without interdependencies.                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| `FileFormat`                      | *(optional)*<br/>Format of your configuration file. Available formats are `yaml`, `toml` and `json`, if you omit the field it will try to unmarshal the file as a `yaml` file.<br/>Default: **`YAML`**                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `LeveledLogger`                   | *(optional)*<br/>LeveledLogger is used to log what `semaphore` is doing.<br/>It should be a `slog` instance.<br/>If no logger is provided the module will not log anything.<br/>Default: **No log**                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `Notifiers`                       | *(optional)*<br/>List of notifiers to call when your flag file has been changed.<br/> *See [notifiers section](notifier.md) for more details*.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| `PollingInterval`                 | (optional) Duration to wait before refreshing the flags.<br/>The minimum polling interval is 1 second.<br/>Default: **60 * time.Second**.<br/>If you set a negative value, the polling will be disabled.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| `EnablePollingJitter`             | (optional) Set to true if you want to avoid having true periodicity when retrieving your flags. It is useful to avoid having spike on your flag configuration storage in case your application is starting multiple instance at the same time.<br/>We ensure a deviation that is maximum ±10% of your polling interval.<br />Default: **false**                                                                                                                                                                                                                                                                                                                                                       |
| `StartWithRetrieverError`         | *(optional)* If **true**, the SDK will start even if we did not get any flags from the retriever. It will serve only default values until the retriever returns the flags.<br/>The init method will not return any error if the flag file is unreachable.<br/>Default: **false**                                                                                                                                                                                                                                                                                                                                                                                                                      |
| `DisableNotifierOnInit`           | *(optional)* If **true**, the SDK will not call any notifier when the flags are loaded during initialization. This is useful if you do not want a Slack/Webhook notification saying that the flags have been added every time you start the application.<br/>Default: **false**                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `Offline`                         | *(optional)* If **true**, the SDK will not try to retrieve the flag file and will not export any data. No notifications will be sent either.<br/>Default: **false**                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| `EvaluationContextEnrichment`     | *(optional)* Free-form `map[string]any` merged with the evaluation context on every evaluation (useful for common attributes like server version or environment).<br/><br/>All keys are added as custom attributes. Matching keys in the request context are overridden by `EvaluationContextEnrichment`. If `Environment` is set, an `env` key in `EvaluationContextEnrichment` is ignored.<br/><br/>Default: **nil** |
| `PersistentFlagConfigurationFile` | *(optional)* If set Semaphore will store the flags configuration in this file to be able to serve the flags even if none of the retrievers is available during starting time.<br/>By default, the flag configuration is not persisted and stays on the retriever system. By setting a file here, you ensure that Semaphore will always start with a configuration but which can be out-dated.<br/><br/>_(example: `/tmp/semaphore_persist_conf.yaml`)_                                                                                                                                                                                                                                         |

## Example
```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    LeveledLogger:   slog.Default(),
    Context:         context.Background(),
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

## Multiple configuration flag files
There is no package-level (singleton) client. Create a client with `ffclient.New` at startup, keep it, and pass it to
the code that evaluates flags. Close it when your application stops.

### Working with multiple semaphore

You can also create many `semaphore` clients to use in your application.  

Each will have its own unique set of configurations and flags. Each can read from a different config file and from different places.  
All the functions are methods of a [`GoFeatureFlag`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#GoFeatureFlag).

#### Example

Inside a function that returns an error, initialize each client and check the result before using it:

```go showLineNumbers
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

// ...
```

When working with multiple [`GoFeatureFlag`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#GoFeatureFlag), it is up to the user to keep track of different [`GoFeatureFlag`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#GoFeatureFlag) instances.

## Offline mode
In some situations, you might want to stop making remote calls and fall back to default values for your feature flags.  
For example, if your software is both cloud-hosted and distributed to customers to run on-premise, it might make sense 
to fall back to defaults when running on-premise.

You can do this by setting `Offline` mode in the client's Config.

## Advanced configuration

- [Export data from your flag variations](data_collection.md)
- [Be notified when your flags change](notifier.md)
