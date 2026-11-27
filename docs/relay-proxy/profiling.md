# Profiling

The relay proxy can expose the Go `net/http/pprof` profiling endpoints under `/debug/pprof/` to help diagnose CPU, memory, and goroutine behavior.

> [!WARNING]
> Profiling endpoints are disabled by default. They are served by the `ops` listener only, never by the `api` listener.
>
> The `ops` listener has **no authentication**, and pprof exposes the contents of the heap (which can hold flag data and keys in memory), the command line and the goroutines. Anyone who can reach the ops port can read them. Keep the ops port off public networks: close it with a network policy or a firewall, and do not publish it from the container. Give the ops listener an address of its own, such as loopback, as shown below.

## Enable profiling

Give the relay an `ops` listener and set `server.ops.pprof.enabled` in the [bootstrap file](bootstrap-config.md):

```yaml
version: 2
server:
  listeners:
    - {name: api, address: ":1031"}
    - {name: ops, address: "127.0.0.1:1032", role: ops}
  ops:
    pprof: {enabled: true}
```

The ops listener binds `127.0.0.1:1032` here, so only the host itself reaches it (from a container, publish the port to the loopback of the host, or use `kubectl port-forward`). The pprof index is then available at `http://localhost:1032/debug/pprof/`. The `api` listener answers `404` for every `/debug/pprof/` path, whatever the setting.

- Without an `ops` listener, the file is refused: `server.ops.pprof.enabled: invalid value: needs a listener with role ops`.
- The log level has no effect on profiling.
- A change of the setting on `SIGHUP` is listed in `pendingRestart` as `server.ops` and applies after a restart.
