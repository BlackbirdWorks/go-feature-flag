# Redis

> [!NOTE]
> The relay proxy does not run retrievers any more: it reads a control plane store (S3 or a directory), see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). The relay configuration examples on this page are version 1 and
> are not read by the relay; the retriever kinds come back to the relay with the control plane connections (issue #57).
> The Go module is not affected.

## Overview

Load flag configurations from Redis using an optional key prefix.

## Redis format

Store each flag as a `string:string` entry where the key is the flag name (optionally prefixed) and the value is the flag configuration serialized as JSON.

On each poll, the retriever:
- Runs Redis `Scan` filtered by `Prefix`
- Reads all matching keys at once and parses each value as a JSON object

## Configure the relay proxy

Add this configuration to your relay proxy config file:

```yaml
# ...
retrievers:
  - kind: redis
    redisOptions:
      addr: "127.0.0.1:6379"
    redisPrefix: "semaphore:"
# ...
```

| Field name     | Mandatory | Type   | Default  | Description |
|----------------|:---------:|--------|----------|-------------|
| `kind`         | Yes       | string | **none** | **Value should be `redis`.**<br/>_Mandatory field identifying the retriever._ |
| `redisOptions` | Yes       | object | **none** | Connection options for your Redis instance. Supports only the options in [`SerializableRedisOptions`](https://github.com/BlackbirdWorks/semaphore/blob/main/cmd/cli/evaluate/retrieverconf/serializable_redis_options.go), including `addr` (required), `username`, `password`, `db`, and `tlsEnabled`. Timeout and retry backoff values are in milliseconds. |
| `redisPrefix`  | No        | string | **none** | Prefix prepended to flag names in Redis. |

## Configure the Go module

Add the retriever to `ffclient.Config{}`:

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &redisretriever.Retriever{
        Prefix: "semaphore:",
        Options: &redis.Options{
            Addr: "127.0.0.1:6379",
        },
    },
})
defer client.Close()
```

| Field         | Mandatory | Description |
|---------------|:---------:|-------------|
| **`Options`** | Yes       | [`redis.Options`](https://pkg.go.dev/github.com/redis/go-redis/v9#Options) struct with connection settings for your Redis instance. |
| **`Prefix`**  | No        | Key prefix used to filter flag keys in Redis. |
