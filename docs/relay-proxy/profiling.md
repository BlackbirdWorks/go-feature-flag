# Profiling

The relay proxy can expose the Go `net/http/pprof` profiling endpoints under `/debug/pprof/` to help diagnose CPU, memory, and goroutine behavior.

> [!WARNING]
> Profiling endpoints are disabled by default. They are served by the `ops` listener only, never by the `api` listener.
>
> The `ops` listener has **no authentication**, and pprof exposes details of the process: the heap profile shows sampled allocation sites and sizes, the goroutine dumps show stacks and their arguments, and the command line is public. This is sensitive for an attacker even though it is not a memory dump. Anyone who can reach the ops port can read them. Keep the ops port off public networks: close it with a network policy or a firewall, and do not publish it from the container. Give the ops listener an address of its own, such as loopback, as shown below.

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

The ops listener binds `127.0.0.1:1032` here, so only the process itself and its host reach it. Inside a container this is not enough to reach it from outside, and there are two cases:

- In a container, bind the ops listener to the container interface (`":1032"`) and restrict who reaches it where the port is published: `docker run -p 127.0.0.1:1032:1032 ...` (a published port that is bound to the host loopback only), or a network policy in Kubernetes. A port published on a bridge network reaches the container address, not the container loopback.
- Loopback inside the container (`127.0.0.1:1032`) is for a sidecar in the same network namespace or for `docker exec` / `kubectl exec` only.

The pprof index is then available at `http://localhost:1032/debug/pprof/`. The `api` listener answers `404` for every `/debug/pprof/` path, whatever the setting.

- Without an `ops` listener, the file is refused: `server.ops.pprof.enabled: invalid value: needs a listener with role ops`.
- The log level has no effect on profiling.
- A change of the setting on `SIGHUP` is listed in `pendingRestart` as `server.ops` and applies after a restart.
