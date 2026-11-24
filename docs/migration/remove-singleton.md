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

## Other Go API changes of the same refactor

Nothing that the relay proxy HTTP API, its configuration keys, its metrics or the CLI output expose changed. The
Go packages changed as follows (nobody outside this repository is expected to import them):

| Before | After |
|---|---|
| process-wide caches of the rule evaluation (parsed queries, scheduled rollouts, compiled JsonLogic, pools) | owned by a `flag.Service`, created by each client (`GoFeatureFlag.EvaluationService()`), passed to evaluations in `flag.Context.Service` |
| `InternalFlag.IsValid()`, `Rule.IsValid(defaultRule, variations)`, `Rule.Evaluate(key, ctx, name, isDefault)` | the same with the `*flag.Service` as first argument |
| `cache.New(notifications, file, logger)`, `cache.NewInMemoryCache(logger)` | `cache.New(..., cache.WithService(svc))` (own Service by default), `cache.NewInMemoryCache(svc, logger)` |
| `legacy.Default`, `Warner.SetLogFunc` | a `legacy.Warner` owned by the relay config loader, the CLI command or the `flag.Service`; `legacy.NewWarner(nil)` drops the warnings |
| `ffcontext.GoffContextSpecifics` | new field `UsedLegacyKey` (the `gofeatureflag` attribute was used) |
| evaluation errors logged through the global `slog` logger | logged through `Config.LeveledLogger` (nothing is logged when the client has no logger) |
| `configfile.ConfigFileDefaultLocations` (variable), `configfile.LoadConfigFile(path, format, locations)` | `ConfigFileDefaultLocations()` (function), `LoadConfigFile(path, format, locations, warner)` |
| `retrieverconf.DefaultRetrieverConfig.{Timeout,HTTPMethod,GitBranch}` | constants `DefaultTimeout`, `DefaultHTTPMethod`, `DefaultGitBranch` |
| `retrieverInit.InitRetriever(conf)` | `InitRetriever(conf, opts...)` with `WithPostgresPools` |
| `postgresqlretriever.GetPool/ReleasePool` (process-wide) | methods of a `PoolRegistry`; `Retriever.Pools` (the relay shares one registry between its flag sets) |
| `config.DefaultExporter` (variable), `kafka.SHA256/SHA512` (variables), `kinesisexporter.DefaultPartitionKey` (variable) | functions with the same names |
| `metric.MetricsOpts` + `promversion.Version = ...` | `MetricsOpts.Version` |
| `OtelService.Init(ctx, logger, config)` setting the global providers and `OTEL_EXPORTER_OTLP_ENDPOINT` | `opentelemetry.ExportEnvironment(config)` and `api.WithGlobalTelemetry()`, both used by `main` only |
| `time.Now()` in the engine | `clock.Now()` (`modules/core/clock`), the only package-level variable of the module |
