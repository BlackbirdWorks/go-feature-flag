# HTTP(S)

> [!NOTE]
> The relay proxy does not run retrievers: it reads its control plane from a store connection, see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). This page is about the retriever of the Go module.

## Overview

Fetch flag configurations from a remote URL over HTTP(S).

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
