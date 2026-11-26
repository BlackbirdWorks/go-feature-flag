# HTTP(S)

## Overview
Fetches the configuration from a remote URL over HTTP(S). This retriever is useful when you want to fetch the configuration from a remote server endpoint.

## Configure the relay proxy

To configure your relay proxy to use the HTTP(S) retriever, you need to add the following
configuration to your relay proxy configuration file:

```yaml
# ...
retrievers:
  - kind: http
    url: https://my-feature-flag-location.com/flags.yaml
# ...
```

| Field name       |    Mandatory     | Type   | Default  | Description                                                                                            |
|------------------|:----------------:|--------|----------|--------------------------------------------------------------------------------------------------------|
| `kind`           |  Yes   | String | **none** | **Value should be `http`**.<br/>_This field is mandatory and describes which retriever you are using._ |
| `url`            |  Yes   | String | **none** | Location to retrieve the file.                                                                         |
| `method`         |  Yes   | String | `GET`    | The HTTP Method you are using to call the HTTP endpoint.                                               |
| `body`           | No | String | **none** | The HTTP Body you are using to call the HTTP endpoint.                                                 |
| `headers`        | No | Object | **none** | The HTTP headers used when calling the HTTP endpoint (useful for authorization).                       |
| `clientCertPath` | No | String | **none** | Path to the client certificate file used for mTLS. Must be configured with `clientKeyPath`.            |
| `clientKeyPath`  | No | String | **none** | Path to the client certificate key file used for mTLS. Must be configured with `clientCertPath`.       |
| `caCertPath`     | No | String | **none** | Path to the CA certificate file used to verify the server certificate.                                 |
| `timeout`        | No | String | `10000`  | Timeout in millisecond when calling the HTTP endpoint.                                                 |

## Configure the Go Module
To configure your Go module to use the HTTP(S) retriever, you need to add the following
configuration to your `ffclient.Config{}` object:

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &httpretriever.Retriever{
        URL:    "http://example.com/flag-config.yaml",
        Timeout: 2 * time.Second,
    },
})
defer client.Close()
```

| Field              |    Mandatory     | Description                                                                                               |
|--------------------|:----------------:|-----------------------------------------------------------------------------------------------------------|
| __URL__            |  Yes   | Location to retrieve the file <br/> _(ex: [http://mydomain.io/flag.yaml](http://mydomain.io/flag.yaml))_. |
| __Method__         | No | the HTTP method you want to use <br/>_(default is `GET`)_.                                                |
| __Body__           | No | If you need a body to get the flags.                                                                      |
| __Header__         | No | Header you should pass while calling the endpoint _(useful for authorization)_.                           |
| __ClientCertPath__ | No | Path to the client certificate file used for mTLS. Must be configured with `ClientKeyPath`.              |
| __ClientKeyPath__  | No | Path to the client certificate key file used for mTLS. Must be configured with `ClientCertPath`.         |
| __CACertPath__     | No | Path to the CA certificate file used to verify the server certificate.                                   |
| __Timeout__        | No | Timeout for the HTTP call <br/>(default is 10 seconds).                                                   |
