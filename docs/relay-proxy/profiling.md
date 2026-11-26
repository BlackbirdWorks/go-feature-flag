# Profiling

The relay proxy can expose Go `net/http/pprof` profiling endpoints under `/debug/pprof/` to help diagnose CPU, memory, and goroutine behavior.

> [!WARNING]
> Profiling endpoints are disabled by default and must be enabled in configuration.

Once enabled, the pprof index is available at `http://localhost:1031/debug/pprof/`.

## Enable profiling

Set `enablePprof: true` in your relay proxy configuration:

```yaml
retriever:
  kind: file
  path: /semaphore/flags.yaml
# ...
enablePprof: true
```

> [!NOTE]
> Setting `logLevel: debug` also enables profiling. Use `enablePprof: true` to enable profiling without debug logging.
>
> ```yaml
> # ...
> logLevel: debug
> ```
