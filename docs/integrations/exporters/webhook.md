# Webhook

## Overview

Export evaluation data by sending HTTP `POST` requests to a webhook.

Every time `FlushInterval` or `MaxEventInMemory` is reached, an HTTP request is sent to the webhook endpoint.

> [!NOTE]
> If the webhook HTTP call fails, events remain in memory and are retried on the next `FlushInterval` or `MaxEventInMemory` flush.

## Webhook format

A `POST` request is sent to `EndpointURL` with a JSON body in this format:

```json
{
    "meta": {
        "hostname": "server01"
    },
    "events": [
        {
            "kind": "feature",
            "contextKind": "anonymousUser",
            "userKey": "14613538188334553206",
            "creationDate": 1618909178,
            "key": "test-flag",
            "variation": "Default",
            "value": false,
            "default": false,
            "source": "SERVER"
        }
    ]
}
```

## Signature

When `secret` / `Secret` is configured, the request includes the `X-Hub-Signature-256` header containing the **HMAC-SHA256 hex digest** of the request body using the secret as the HMAC key.

> [!CAUTION]
> Always configure `secret` / `Secret` and verify `X-Hub-Signature-256` on your webhook endpoint to protect against tampering and man-in-the-middle attacks.

## Configure the relay proxy

Add the following configuration to your relay proxy configuration file:

```yaml
# ...
exporters:
  - kind: webhook
    endpointUrl: https://your-webhook-url.com/
# ...
```

| Field name         | Mandatory | Type                | Default  | Description                                                                                               |
|--------------------|:---------:|---------------------|----------|-----------------------------------------------------------------------------------------------------------|
| `kind`             | Yes       | string              | **none** | Value should be `webhook`.<br/>_This field is mandatory and describes which exporter you are using._        |
| `endpointUrl`      | Yes       | string              | **none** | Target URL of your webhook endpoint.                                                                      |
| `flushInterval`    | No        | int                 | `60000`  | Interval in milliseconds before flushing events (flushes earlier if `maxEventInMemory` is reached first). |
| `maxEventInMemory` | No        | int                 | `100000` | Maximum number of events buffered in memory before flushing.                                              |
| `secret`           | No        | string              | **none** | Secret used to sign the request body and populate `X-Hub-Signature-256` (see [Signature](#signature)).    |
| `meta`             | No        | map[string]string   | **none** | Extra key-value metadata included in the request payload.                                                 |
| `headers`          | No        | map[string][]string | **none** | Custom HTTP headers sent with each webhook request.                                                       |

## Configure the Go module

Add the following configuration to your `ffclient.Config{}` object:

```go
ffConfig := ffclient.Config{
    // ...
    DataExporter: ffclient.DataExporter{
        // ...
        Exporter: &webhookexporter.Exporter{
            EndpointURL: "https://webhook.url/",
            Secret:      "secret-for-signing",
            Meta: map[string]string{
                "extraInfo": "info",
            },
            Headers: map[string][]string{
                "Authorization": {"Bearer auth_token"},
            },
        },
    },
    // ...
}
client, err := ffclient.New(ffConfig)
defer client.Close()
```

| Field         | Mandatory | Description                                                                                             |
|---------------|:---------:|---------------------------------------------------------------------------------------------------------|
| `EndpointURL` | Yes       | Target URL of your webhook endpoint.                                                                    |
| `Secret`      | No        | Secret used to sign the request body and populate `X-Hub-Signature-256` (see [Signature](#signature)).  |
| `Meta`        | No        | Extra key-value metadata included in the request payload.                                               |
| `Headers`     | No        | Custom HTTP headers sent with each webhook request.                                                     |
