# Profiling

The relay proxy can expose the Go `net/http/pprof` profiling endpoints under `/debug/pprof/` to help diagnose CPU, memory, and goroutine behavior.

> [!WARNING]
> Profiling endpoints are disabled by default. They are served by the `ops` listener only, never by the `api` listener, so they stay off the network that serves the flags.

## Enable profiling

Give the relay an `ops` listener and set `server.ops.pprof.enabled` in the [bootstrap file](bootstrap-config.md):

```yaml
version: 2
server:
  listeners:
    - {name: api, address: ":1031"}
    - {name: ops, address: ":1032", role: ops}
  ops:
    pprof: {enabled: true}
```

The pprof index is then available at `http://localhost:1032/debug/pprof/`. The `api` listener answers `404` for every `/debug/pprof/` path, whatever the setting.

- Without an `ops` listener, the file is refused: `server.ops.pprof.enabled: invalid value: needs a listener with role ops`.
- The log level has no effect on profiling.
- A change of the setting on `SIGHUP` is listed in `pendingRestart` as `server.ops` and applies after a restart.
