# Redis

> [!NOTE]
> The relay proxy does not run retrievers: it reads its control plane from a store connection, see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). This page is about the retriever of the Go module.

## Overview

Load flag configurations from Redis using an optional key prefix.

## Redis format

Store each flag as a `string:string` entry where the key is the flag name (optionally prefixed) and the value is the flag configuration serialized as JSON.

On each poll, the retriever:
- Runs Redis `Scan` filtered by `Prefix`
- Reads all matching keys at once and parses each value as a JSON object

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
