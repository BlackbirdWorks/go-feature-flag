# Connections

> Status: the connection factory is in the code base but not active yet. It becomes active when the relay switches
> to config v2 (epic #19, issue #3). The v1 configuration keeps building its AWS clients as before.

## What a connection is

A **connection** is a named backend with its own endpoint, region, credentials, timeouts and retry settings. For S3 it
is one bucket. Several connections (other buckets, endpoints, accounts) are the normal case, and components refer to
them by name.

## One client per connection

- Exactly one AWS client exists per connection per process. The factory builds it on first use and hands the same
  instance to every caller, so the connection pool, the retryer and the request metric label are one per connection.
- Two connections never share a client, even with equal settings.
- The factory counts the clients it built per connection, so a test can assert the invariant.
- Endpoint and region are per connection: two connections can target two emulators in one process, with no
  environment variable set and no global state.
- The spec is the only source of region and endpoint. `region` is required. `AWS_REGION`, `AWS_ENDPOINT_URL`,
  `AWS_ENDPOINT_URL_S3`, `AWS_DEFAULTS_MODE`, `AWS_PROFILE` and the shared config files are ignored. The only
  exception is the credentials chain (see below).
- Connection names must not be empty.
- `AWS_CA_BUNDLE`, the proxy variables and every other process-level AWS setting do not affect a connection, except
  as the source of `chain` credentials (and of `assumeRole` without source keys): the SDK default chain is used
  only to obtain a credentials provider. The data-plane HTTP client is built from the spec. It does not use a
  proxy; use `caBundleFile` for private CAs.

## Fields

S3 connection:

| Field | Default | Meaning |
|---|---|---|
| `bucket` | none | Bucket of the connection (required by the components that use one) |
| `endpoint` | AWS endpoint of the region | Base URL, for emulators and S3 compatible stores |
| `region` | required | AWS region |
| `pathStyle` | `false` | Path-style addressing |
| `credentials` | `chain` | See below |
| `caBundleFile` | none | PEM file of extra certificate authorities, trusted in addition to the system roots (TLS emulators, private stores). It is read and parsed once, when the spec is accepted: a missing, empty or certificate-less file refuses the `Set`/`Replace`, and a file that changes or disappears later has no effect until the next reload. The factory identity includes a digest of the content, so a changed bundle rebuilds the client on the next reload |
| `connectTimeout` | 30s | TCP connect timeout only (the TLS handshake has a fixed 10s limit) |
| `requestTimeout` | none | Wait for the response headers of one HTTP attempt. It does not cut the reading of a body: only the caller's context does |
| `retry.maxAttempts` | SDK default (3) | Attempts per operation, the first one included |
| `retry.maxBackoff` | SDK default (20s) | Longest wait between two attempts |
| `requestChecksumCalculation` | SDK default (`whenSupported`) | `whenSupported` or `whenRequired` |
| `responseChecksumValidation` | SDK default (`whenSupported`) | `whenSupported` or `whenRequired` |

The two checksum settings exist for S3 compatible stores that reject the checksum headers the SDK sends by default.

Kinesis, SQS and Redshift connections take the same fields except `bucket`, `pathStyle` and the checksum settings.

## Credentials

| Type | Fields | Behaviour |
|---|---|---|
| `chain` (default) | none | The SDK default chain (environment, shared files, container and instance roles). The only type that reads the process environment |
| `static` | `accessKeyId`, `secretAccessKey`, `sessionToken` | Fixed keys |
| `assumeRole` | `roleArn`, `sessionName`, `externalId`, `stsEndpoint`, optional static source keys | Assumes the role; the source is the static keys when given (`accessKeyId` and `secretAccessKey` together), the chain otherwise |
| `webIdentity` | `roleArn`, `tokenFile`, `sessionName`, `stsEndpoint` | Assumes the role with a web identity token file |

- Secret values arrive already resolved, as `connection.Secret` (`NewSecret`, `Reveal`). Every fmt verb, JSON (also as a map key), YAML and slog print `[REDACTED]`. A `Secret` is not meant to be a map key: as a key it encodes to the same marker, so entries collide (deliberate, the value is never emitted). `%p`, reflection and `unsafe` cannot be covered.
- The credentials provider in the returned AWS config prints `[REDACTED]` too.
- Limit of the `chain` type: the SDK validates every `AWS_*` variable and the shared files when it loads the chain, and no option skips that. An invalid variable (even an unrelated one such as `AWS_DEFAULTS_MODE`) makes the chain connection fail to build, with an error naming the connection. Static, `webIdentity` and `assumeRole` with source keys never load the chain.
- The STS endpoint is separate from the connection endpoint.
- STS calls are not counted in the request metric.

## Identity and reload

Every connection has an identity: a fingerprint of everything that affects its client (endpoint, region, bucket,
path style, credential settings including a hash of the secret values, timeouts, retry, checksum settings). It never
contains a secret in clear. Credential fields that the chosen type ignores do not count, and an empty type equals
`chain`.

On a reload the factory compares identities:

- unchanged: the same client is kept, nothing is rebuilt;
- changed: a new client is built on next use;
- removed: the connection is dropped;
- dropped or replaced: the idle connections of the old client are closed;
- invalid: the whole reload is refused and nothing changes.

## Request metric

Every HTTP attempt made through a connection client is reported once (a request retried after a 503 counts twice).
The clients do not follow redirects, so a redirect is one observation, not several.

An observation is an attempt made by the SDK, not a write on the wire. The Go HTTP transport may transparently
replay an idempotent request (`GET`, `HEAD`) on a kept-alive connection that died before any response byte; the
replay is not observed again. The first send got no response, so the service processed at most one of them. No
transport setting avoids this without disabling keep-alive, which the clients keep.
The relay exposes it as `semaphore_s3_requests_total{connection,operation,result}`.

`operation` is `get`, `list`, `head`, `put`, `delete` or `other`.

| `result` | HTTP outcome |
|---|---|
| `ok` | 2xx |
| `not_modified` | 304 answer to a conditional request (`If-None-Match`); not a failure |
| `not_found` | 404 |
| `throttled` | 429, or a throttling error code such as `SlowDown` (also on a 503) |
| `redirect` | any 3xx other than 304 (not followed) |
| `client_error` | any other 4xx (403, 400, ...) |
| `server_error` | 5xx that is not a throttle |
| `error` | no usable answer: transport error, timeout, cancellation, undecodable 2xx |

The SDK returns a 304 as an error. `connection.IsNotModified(err)` tells it from a failure; callers check it before
any logging or counting.
