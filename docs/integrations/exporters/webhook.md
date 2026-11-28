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

Add the exporter to the `exporters` map of the [bootstrap file](../../relay-proxy/bootstrap-config.md):

```yaml
version: 2
exporters:
  evaluations:
    kind: webhook
    endpointUrl: {fromEnv: EVALUATION_WEBHOOK_URL}
    secret: {fromEnv: EVALUATION_WEBHOOK_SECRET}
    headers:
      X-Source: {fromEnv: EVALUATION_WEBHOOK_SOURCE}
```

- `endpointUrl` (required, a secret reference).
- Optional: `secret` (signs the body, see [Signature](#signature)) and `headers` (a map of secret references).

The settings that every exporter takes (`flushInterval`, `maxEventInMemory`, `eventType`, `meta`, `overflowPolicy`, `retry` and the other pipeline settings) are in the [bootstrap file reference](../../relay-proxy/bootstrap-config.md) (section `exporters.<name>`).

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
