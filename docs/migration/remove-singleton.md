# Migration: removal of the package-level singleton client

The Go module no longer keeps a process-wide client. State (the cache, the exporters, and later the
evaluation caches) is owned by the `GoFeatureFlag` object that `ffclient.New` returns, and is passed
down to whoever needs it. The relay proxy and the CLI never used the singleton, so the HTTP API, the
configuration keys and the CLI output are unchanged.

## Removed

| Removed package function | Use instead |
|---|---|
| `ffclient.Init(cfg)` | `client, err := ffclient.New(cfg)` |
| `ffclient.Close()` | `client.Close()` |
| `ffclient.BoolVariation`, `IntVariation`, `Float64Variation`, `StringVariation`, `JSONVariation`, `JSONArrayVariation` and their `...Details` forms | the methods with the same name on the client |
| `ffclient.AllFlagsState`, `ffclient.GetFlagsFromCache`, `ffclient.GetFlagsFromCacheWithContext` | the methods with the same name on the client |
| `ffclient.Track` | `client.Track` |
| `ffclient.SetOffline`, `ffclient.SetOfflineWithContext`, `ffclient.IsOffline` | the methods on the client (`SetOfflineWithContext` is new on the client) |
| `ffclient.GetCacheRefreshDate`, `ffclient.ForceRefresh` | the methods on the client (`ForceRefresh` returns `false` while offline) |

```go
client, err := ffclient.New(ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever:       &fileretriever.Retriever{Path: "flag-config.yaml"},
})
if err != nil {
    return err
}
defer client.Close()

enabled, _ := client.BoolVariation("my-flag", ffcontext.NewEvaluationContext("user-key"), false)
```
