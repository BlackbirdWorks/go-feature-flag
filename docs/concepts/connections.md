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

## Fields

S3 connection:

| Field | Default | Meaning |
|---|---|---|
| `bucket` | none | Bucket of the connection (required by the components that use one) |
| `endpoint` | AWS | Base URL, for emulators and S3 compatible stores |
| `region` | SDK chain | AWS region |
| `pathStyle` | `false` | Path-style addressing |
| `credentials` | `chain` | See below |
| `connectTimeout` | SDK default | TCP connect timeout |
| `requestTimeout` | none | Timeout of one HTTP attempt |
| `retry.maxAttempts` | SDK default (3) | Attempts per operation, the first one included |
| `retry.maxBackoff` | SDK default (20s) | Longest wait between two attempts |
| `requestChecksumCalculation` | SDK default (`whenSupported`) | `whenSupported` or `whenRequired` |
| `responseChecksumValidation` | SDK default (`whenSupported`) | `whenSupported` or `whenRequired` |

The two checksum settings exist for S3 compatible stores that reject the checksum headers the SDK sends by default.

Kinesis, SQS and Redshift connections take the same fields except `bucket`, `pathStyle` and the checksum settings.

## Credentials

| Type | Fields | Behaviour |
|---|---|---|
| `chain` (default) | none | The SDK default chain (environment, shared files, container and instance roles) |
| `static` | `accessKeyId`, `secretAccessKey`, `sessionToken` | Fixed keys |
| `assumeRole` | `roleArn`, `sessionName`, `externalId`, `stsEndpoint`, optional static source keys | Assumes the role; the source is the static keys when given, the chain otherwise |
| `webIdentity` | `roleArn`, `tokenFile`, `sessionName`, `stsEndpoint` | Assumes the role with a web identity token file |

- Secret values arrive already resolved. They print as `[REDACTED]` in logs, JSON, errors and `%v`, `%+v`, `%#v`.
- The STS endpoint is separate from the connection endpoint.
- STS calls are not counted in the request metric.

## Identity and reload

Every connection has an identity: a fingerprint of everything that affects its client (endpoint, region, bucket,
path style, credential settings including a hash of the secret values, timeouts, retry, checksum settings). It never
contains a secret in clear.

On a reload the factory compares identities:

- unchanged: the same client is kept, nothing is rebuilt;
- changed: a new client is built on next use;
- removed: the connection is dropped;
- invalid: the whole reload is refused and nothing changes.

## Request metric

Every HTTP attempt made through a connection client is reported once (a request retried after a 503 counts twice).
The relay exposes it as `semaphore_s3_requests_total{connection,operation,result}`.

`operation` is `get`, `list`, `head`, `put`, `delete` or `other`.

| `result` | HTTP outcome |
|---|---|
| `ok` | 2xx |
| `not_modified` | 304 answer to a conditional request (`If-None-Match`); not a failure |
| `not_found` | 404 |
| `throttled` | 429, or a throttling error code such as `SlowDown` (also on a 503) |
| `client_error` | any other 4xx (403, 400, ...) |
| `server_error` | 5xx that is not a throttle |
| `error` | no usable answer: transport error, timeout, cancellation, undecodable 2xx |

The SDK returns a 304 as an error. `connection.IsNotModified(err)` tells it from a failure; callers check it before
any logging or counting.
