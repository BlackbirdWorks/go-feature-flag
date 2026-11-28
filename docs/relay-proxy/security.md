# Security

## Two kinds of key

| Key | Where it lives | What it allows |
|---|---|---|
| Admin key | `auth.adminKeys` in the bootstrap file. A plain string, `{fromEnv: NAME}` or `{fromFile: PATH}`. | The admin endpoints (`X-API-Key` header, see [advanced usage](advanced-usage.md)). It does not evaluate flags: a flag handler refuses it. |
| API key of a flag set | `apiKeys` in the flag-set document, a list of plain strings. | Evaluation of the flags of that one flag set. It never opens an admin endpoint. |

- The bootstrap file is never published. Admin keys never appear in the manifest or in a document.
- A key that is both an admin key and the API key of a flag set grants nothing while the clash exists. See the
  [probes page](probes.md#admin-key-equal-to-a-flag-set-key).
- A request with an unknown key, an empty key or the key of a removed flag set gets `401`, with no hint of the reason.
- Prefer `fromEnv` or `fromFile` for admin keys and for every secret of a connection. A secret that you write in the
  file is plain text in the file.

## Plain keys in published documents

The flag-set document holds the API keys as plain strings, as the configuration file of version 1 did. Whoever can read
the object can use the keys. The protection is the store:

- Give the relay read access to the control-plane bucket and nothing more. Give write access to the publisher only.
- Turn on encryption at rest on the bucket.
- Restrict who can list and read the bucket, as for any secret.
- Rotate a key by publishing a new document, see the [runbook](runbook.md#rotate-an-api-key).

API keys of an SDK client are not secrets of the same weight as an admin key: they identify a flag set and can be read
by a client of that flag set. Treat the admin keys as secrets.

## What is never logged or shown

- Admin keys and API keys never appear in a log line, an error, `config print`, `/info` or a metric label. They show as
  `[REDACTED]` where a value would print. `/info` counts keys (`config.flagSets[].apiKeys`) and never lists them.
- A secret reference (`fromEnv`, `fromFile`) prints as written and is not resolved by `config print`.
- An error of the bootstrap file names the path of a key and its line. Under `auth` an unknown key is reported by path
  and line only.
- `/info` shows closed codes. The free text of a failure can quote the content of an object, so it goes to the logs only,
  and only in the `detail` of a rejected `"runtime reload"` line.
- A validation error of a flag-set document never prints a key; a duplicate names the path of the first use.
- A URL that is printed must not carry credentials; the file is refused when it does.

## State directory

`controlPlane.stateDir` holds a copy of what the relay serves, including the flag-set documents with their plain API
keys. Treat it as sensitive.

- The relay creates the directory with mode `0700` and its files with mode `0600`. An existing directory keeps its mode.
- The relay refuses a directory that is a symbolic link or that any user can write to. The owner does not matter, so a
  volume that Kubernetes mounts with `fsGroup` works.
- Give the directory to the relay user only. Do not share it between services.
- The files are written to a temporary name and renamed, so a crash leaves the previous state readable.

## Transport

- The relay listeners speak plain HTTP (`http1`, and `h2c` on request). `h2` and `tls` are reserved and not available.
  Terminate TLS in front of the relay (ingress, load balancer or sidecar), and keep the `ops` listener on a private
  address.
- Connections to S3 use HTTPS unless `endpoint` says `http://`. Use `http://` for a local emulator only. Add
  `caBundleFile` for a private certificate authority, see [connections](../concepts/connections.md).
- Connections to Redis and Valkey have TLS off until `tls.enable` is true; the keys `tls.serverName` and
  `tls.caBundleFile` set the name and the authorities.
- Credentials of a connection come from the default credential chain, a role, or secrets (`fromEnv`, `fromFile`). Give
  the role the least access: read on the control-plane bucket.
