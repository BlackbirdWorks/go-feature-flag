# HTTP(S)

> [!NOTE]
> The relay proxy does not run retrievers any more: it reads a control plane store (S3 or a directory), see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). The relay configuration examples on this page are version 1 and
> are not read by the relay; the retriever kinds come back to the relay with the control plane connections (issue #57).
> The Go module is not affected.

## Overview

Fetch flag configurations from a remote URL over HTTP(S).

## Configure the relay proxy

Add this configuration to your relay proxy config file:

```yaml
# ...
retrievers:
  - kind: http
    url: https://my-feature-flag-location.com/flags.yaml
# ...
```

| Field name       | Mandatory | Type   | Default  | Description |
|------------------|:---------:|--------|----------|-------------|
| `kind`           | Yes       | string | **none** | **Value should be `http`.**<br/>_Mandatory field identifying the retriever._ |
| `url`            | Yes       | string | **none** | Location to retrieve the file. |
| `method`         | No        | string | `GET`    | HTTP method used to call the endpoint. |
| `body`           | No        | string | **none** | HTTP request body sent when calling the endpoint. |
| `headers`        | No        | object | **none** | HTTP headers sent when calling the endpoint (useful for authorization). |
| `clientCertPath` | No        | string | **none** | Path to the client certificate file used for mTLS. Must be set with `clientKeyPath`. |
| `clientKeyPath`  | No        | string | **none** | Path to the client certificate key file used for mTLS. Must be set with `clientCertPath`. |
| `caCertPath`     | No        | string | **none** | Path to the CA certificate file used to verify the server certificate. |
| `timeout`        | No        | int    | `10000`  | Timeout in milliseconds when calling the HTTP endpoint. |

## Configure the Go module

Add the retriever to `ffclient.Config{}`:

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &httpretriever.Retriever{
        URL:     "http://example.com/flag-config.yaml",
        Timeout: 2 * time.Second,
    },
})
defer client.Close()
```

| Field                | Mandatory | Description |
|----------------------|:---------:|-------------|
| **`URL`**            | Yes       | Location to retrieve the file _(ex: `http://mydomain.io/flag.yaml`)_. |
| **`Method`**         | No        | HTTP method to use.<br/>Default: `GET` |
| **`Body`**           | No        | Request body to send when fetching flags. |
| **`Header`**         | No        | HTTP headers (`http.Header`) passed when calling the endpoint _(useful for authorization)_. |
| **`ClientCertPath`** | No        | Path to the client certificate file used for mTLS. Must be set with `ClientKeyPath`. |
| **`ClientKeyPath`**  | No        | Path to the client certificate key file used for mTLS. Must be set with `ClientCertPath`. |
| **`CACertPath`**     | No        | Path to the CA certificate file used to verify the server certificate. |
| **`Timeout`**        | No        | Timeout for the HTTP call.<br/>Default: `10 seconds` |
