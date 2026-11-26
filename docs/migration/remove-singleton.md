# Migration: removal of the package-level singleton client

The Go module no longer keeps a process-wide client. State (the cache, exporters, and evaluation caches) is owned by the `GoFeatureFlag` instance returned by `ffclient.New` and passed to callers that need it. The relay proxy and CLI never used the singleton, so the HTTP API, configuration keys, and CLI output are unchanged.

## Removed

| Removed package function | Use instead |
|---|---|
| `ffclient.Init(cfg)` | `client, err := ffclient.New(cfg)` |
| `ffclient.Close()` | `client.Close()` |
| `ffclient.BoolVariation`, `IntVariation`, `Float64Variation`, `StringVariation`, `JSONVariation`, `JSONArrayVariation` and their `...Details` forms | The methods with the same name on the client |
| `ffclient.AllFlagsState`, `ffclient.GetFlagsFromCache`, `ffclient.GetFlagsFromCacheWithContext` | The methods with the same name on the client |
| `ffclient.Track` | `client.Track` |
| `ffclient.SetOffline`, `ffclient.SetOfflineWithContext` | `client.SetOfflineWithContext(ctx, offline)`, which (like the package function) stops background polling when going offline. Going back online does not restart polling (a known limitation of the package function too: the background updater is closed for good; create a new client instead). `client.SetOffline(offline)` only switches the offline setting and leaves polling running |
| `ffclient.IsOffline` | `client.IsOffline` |
| `ffclient.GetCacheRefreshDate`, `ffclient.ForceRefresh` | The methods on the client (`ForceRefresh` returns `false` while offline) |

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever:       &fileretriever.Retriever{Path: "flag-config.yaml"},
})
if err != nil {
    return err
}
defer client.Close()

enabled, _ := client.BoolVariation("my-flag", ffcontext.NewEvaluationContext("user-key"), false)
```

## Other Go API changes of the same refactor

Nothing exposed by the relay proxy HTTP API, configuration keys, metrics, or CLI output changed. Internal Go packages changed as follows (nothing outside this repository is expected to import them):

| Before | After |
|---|---|
| Process-wide caches of rule evaluation (parsed queries, scheduled rollouts, compiled JsonLogic, pools) | Owned by a `flag.Service`, created by each client (`GoFeatureFlag.EvaluationService()`), passed to evaluations in `flag.Context.Service` |
| `InternalFlag.IsValid()`, `Rule.IsValid(defaultRule, variations)`, `Rule.Evaluate(key, ctx, name, isDefault)` | Same signatures with `*flag.Service` as the first argument |
| `cache.New(notifications, file, logger)`, `cache.NewInMemoryCache(logger)` | `cache.New(..., cache.WithService(svc))` (own `Service` by default), `cache.NewInMemoryCache(svc, logger)` |
| `legacy.Default`, `Warner.SetLogFunc` | `legacy.Warner` owned by the relay config loader, CLI command, or `flag.Service`; `legacy.NewWarner(nil)` drops warnings |
| `ffcontext.GoffContextSpecifics` | New field `UsedLegacyKey` (`true` when the `gofeatureflag` attribute was used) |
| Evaluation errors logged through the global `slog` logger | Logged through `Config.LeveledLogger` (nothing is logged when the client has no logger) |
| `configfile.ConfigFileDefaultLocations` (variable), `configfile.LoadConfigFile(path, format, locations)` | `ConfigFileDefaultLocations()` (function), `LoadConfigFile(path, format, locations, warner)` |
| `retrieverconf.DefaultRetrieverConfig.{Timeout,HTTPMethod,GitBranch}` | Constants `DefaultTimeout`, `DefaultHTTPMethod`, `DefaultGitBranch` |
| `retrieverInit.InitRetriever(conf)` | `InitRetriever(conf, opts...)` with `WithPostgresPools` |
| `postgresqlretriever.GetPool/ReleasePool` (process-wide) | Methods of `PoolRegistry`; `Retriever.Pools` (the relay shares one registry between its flag sets) |
| `config.DefaultExporter` (variable), `kafka.SHA256/SHA512` (variables), `kinesisexporter.DefaultPartitionKey` (variable) | Functions with the same names |
| `metric.MetricsOpts` + `promversion.Version = ...` | `MetricsOpts.Version` |
| `OtelService.Init(ctx, logger, config)` setting global providers and `OTEL_EXPORTER_OTLP_ENDPOINT` | `opentelemetry.ExportEnvironment(config)` and `api.WithGlobalTelemetry()`, both used by `main` only |
| `time.Now()` in the engine | `clock.Now()` (`modules/core/clock`), the only package-level variable of the module |
