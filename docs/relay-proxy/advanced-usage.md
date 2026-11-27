# Advanced usage

## Manually trigger retrievers and refresh the internal flag cache

> [!WARNING]
> The relay proxy does not refresh on request any more: `POST /admin/v1/retriever/refresh` answers `501`. The relay polls the
> control plane manifest and applies a change after one poll, see [the core](relay-core.md). The text below describes the
> version 1 behaviour.

By default, retrievers refresh configuration on the configured polling interval. You can also force an immediate refresh (for example, in CI after updating a flag file) by sending a `POST` request to `/admin/v1/retriever/refresh`:

```shell
curl -X 'POST' \
  'http://<your_domain>:1031/admin/v1/retriever/refresh' \
  -H 'accept: application/json' \
  -H 'X-API-Key: <your_admin_api_key>' \
  -d ''
```

> [!NOTE]
> This endpoint requires an **admin token** configured under `authorizedKeys.admin`:
>
> ```yaml
> # ...
> authorizedKeys:
>   admin:
>     - "YOUR_API_KEY"
> ```

## Validate a flag file

Before deploying a flag file, validate it with a `POST` request to `/admin/v1/flags/validate`. This applies the same rules as `semaphore-cli lint` with no side effects—served flags are not modified.

Send the flag file as the request body and set `Content-Type` to select the format:

- `application/json` (default max body: 1 MiB)
- `application/yaml`, `application/x-yaml`, or `text/yaml` (default max body: 64 KiB)
- `application/toml` (default max body: 64 KiB)

Body limits are configurable via [`endpoints.validateFlags`](configuration.md#endpointsvalidateflags) (`maxJSONBodySize`, `maxYAMLBodySize`, and `maxTOMLBodySize`) as bytes or sizes like `1MiB`. Raising YAML or TOML limits makes parsing more expensive because cost grows quadratically with body size. An empty body, `null`, or `{}` returns `200` with `valid: true` and no flags (an empty JSON body is a syntax error and returns `400`).

```shell
curl -X 'POST' \
  'http://<your_domain>:1031/admin/v1/flags/validate' \
  -H 'Content-Type: application/yaml' \
  -H 'X-API-Key: <your_admin_api_key>' \
  --data-binary @flags.yaml
```

```json
{
  "version": "v1.0.0",
  "valid": false,
  "errors": [{ "flag": "my-flag", "message": "missing default rule" }]
}
```

| Status | Meaning                                                              |
|--------|----------------------------------------------------------------------|
| `200`  | All the flags are valid.                                             |
| `422`  | At least one flag is invalid, see `errors` (sorted by flag).         |
| `400`  | The body can't be parsed.                                            |
| `413`  | The body is larger than the limit of its format.                     |
| `415`  | The `Content-Type` header is missing or unsupported.                 |

The validation matches `semaphore-cli lint` and shares the same known gaps:

- `scheduledRollout` steps are not validated.
- Unknown or misspelled fields are ignored.
- Readable files with wrongly typed fields return `400` (parse error), not `422`.

Like other admin endpoints, this requires an **admin token**.

## FIPS 140-3 mode

The relay proxy can be built in **FIPS 140-3** mode for FedRAMP-authorized environments requiring FIPS 140 validated cryptography (controls **SC-13** / **SC-8**).

FIPS builds use Go's native FIPS mode (`GOFIPS140`, Go 1.24+) backed by the [Go Cryptographic Module](https://go.dev/doc/security/fips140) (going through CMVP validation). Building with `GOFIPS140=v1.0.0` sets `GODEBUG=fips140=on` by default, so the binary runs in FIPS mode out of the box without extra runtime configuration.

The pinned module version lives in [`.fips-version`](https://github.com/BlackbirdWorks/semaphore/blob/main/.fips-version) at the repository root, read by the Taskfile target.

> [!NOTE]
> **Scope**
> **Inbound TLS is out of scope.** The relay proxy serves plain HTTP and expects TLS termination at the ingress or service mesh, which handles north-south FIPS-validated TLS.
>
> The FIPS build governs **outbound** cryptography: pulling flag configuration over HTTPS, webhook notifiers, and OTLP export.

### Building from source

```shell
task build-relayproxy-fips
```

This produces `out/bin/semaphore-fips`, built with `GOFIPS140=v1.0.0`.

### Verifying FIPS mode

**At build level** — confirm the binary was compiled against the Go Cryptographic Module:

```shell
go version -m ./out/bin/semaphore-fips | grep GOFIPS140
# build	GOFIPS140=v1.0.0-<commit>
```

The Go toolchain appends the module's commit hash (e.g. `v1.0.0-c2097c7c`), so the
presence of a `GOFIPS140` build setting is what confirms the FIPS module was linked.

**At runtime** — on startup the relay proxy logs whether FIPS crypto is active:

```json
{"level":"info","msg":"crypto mode","fips140":true}
```

A standard (non-FIPS) build logs `"fips140":false`.

**Strict enforcement (optional)** — run with `GODEBUG=fips140=only` to make the process
fail if any non-FIPS-approved cryptographic algorithm is used:

```shell
GODEBUG=fips140=only ./out/bin/semaphore-fips --config=/semaphore/config.yaml
```

### Recommended hardening for FedRAMP deployments

The FIPS build covers cryptography only. The released images already run as the non-root user 65532; in addition
run the container with all capabilities dropped, no privilege escalation, the `RuntimeDefault` seccomp profile and,
where your setup allows it, a read-only root filesystem. If you use the file exporter or
`persistentFlagConfigurationFile` (both write files), keep the root filesystem read-only and mount a writable
volume for those paths, owned by or writable for uid 65532. Combine this with your
platform's pod security standards as appropriate.
