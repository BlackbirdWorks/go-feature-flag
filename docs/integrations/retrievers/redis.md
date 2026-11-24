# Redis

## Overview
Load the configuration from Redis using a specific prefix.

## Redis Format
If you use Redis to store your flags, you need a specific format to store your flags.

We expect the flag to be stored as a `string:string` format where the key is the flag key (with or without a prefix) and the value is a string representing the flag in JSON.

The retriever will `Scan` redis filtering with the `Prefix` and will parse the value as a `JSON object`.

The retriever will read all the flags from Redis at once.

## Configure the relay proxy

To configure your relay proxy to use the Redis retriever, you need to add the following
configuration to your relay proxy configuration file:

```yaml
# ...
retrievers:
  - kind: redis
    redisOptions:
      addr: "127.0.0.1:6379"
    redisPrefix: "semaphore:"
# ...
```
| Field name |    Mandatory     | Type   | Default  | Description                                                                                                                                                                                                                           |
|------------|:----------------:|--------|----------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `kind`     |  Yes   | string | **none** | **Value should be `redis`**.<br/>_This field is mandatory and describes which retriever you are using._                                                                                                                               |
| `redisOptions` | Yes | object | **none** | Connection options for your Redis instance. The relay proxy supports only the options defined in [`SerializableRedisOptions`](https://github.com/BlackbirdWorks/semaphore/blob/main/cmdhelpers/retrieverconf/serializable_redis_options.go), including `addr` (required), `username`, `password`, `db`, and `tlsEnabled`. Timeout and retry backoff values are in milliseconds. |
| `redisPrefix` | No | string | **none** | Prefix used before your flag name in the Redis DB. |

## Configure the GO Module
To configure your GO module to use the Redis retriever, you need to add the following
configuration to your `ffclient.Config{}` object:

```go
client, err := ffclient.New(ffclient.Config{
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

| Field         |    Mandatory     | Description                                                                                                                                      |
|---------------|:----------------:|--------------------------------------------------------------------------------------------------------------------------------------------------|
| **`Options`** |  Yes   | A [`redis.Options`](https://pkg.go.dev/github.com/redis/go-redis/v9#Options) object containing the connection information to the redis instance. |
| **`Prefix`**  | No | Key prefix to filter on the key names.                                                                                                           |
