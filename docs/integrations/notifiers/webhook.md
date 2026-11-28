# Webhook

## Overview

The Webhook notifier sends an HTTP `POST` request to your endpoint every time Semaphore detects a flag configuration change.

- See [Format](#format) for the JSON payload.
- See [Signature](#signature) to verify request authenticity.

## Format

Semaphore sends a `POST` request to `EndpointURL` with the following JSON structure:

```json
{
  "meta": {
    "hostname": "server01"
    // ...
  },
  "flags": {
    "deleted": {},
    // map of your deleted flags
    "added": {},
    // map of your added flags
    "updated": {
      "flag-name": {
        // an object that contains old and new value
        "old_value": {},
        "new_value": {}
      }
    }
  }
}
```

### Example

```json
{
  "meta": {
    "hostname": "server01"
  },
  "flags": {
    "deleted": {
      "test-flag": {
        "rule": "key eq \"random-key\"",
        "percentage": 100,
        "true": true,
        "false": false,
        "default": false
      }
    },
    "added": {
      "test-flag3": {
        "percentage": 5,
        "true": "test",
        "false": "false",
        "default": "default"
      }
    },
    "updated": {
      "test-flag2": {
        "old_value": {
          "rule": "key eq \"not-a-key\"",
          "percentage": 100,
          "true": true,
          "false": false,
          "default": false
        },
        "new_value": {
          "disable": true,
          "rule": "key eq \"not-a-key\"",
          "percentage": 100,
          "true": true,
          "false": false,
          "default": false
        }
      }
    }
  }
}
```

## Signature

When a secret is configured, Semaphore sends the `X-Hub-Signature-256` header containing the SHA-256 HMAC hex digest of the request body signed with `Secret`.

Notifications can be delivered concurrently, so always verify each request's signature against that request's body.

> [!CAUTION]
> Always configure `Secret` and verify `X-Hub-Signature-256` on your webhook endpoint to protect against tampering and man-in-the-middle attacks.

## Configure the relay proxy

Add the notifier to the `notifiers` map of the [bootstrap file](../../relay-proxy/bootstrap-config.md):

```yaml
version: 2
notifiers:
  flag-changes:
    kind: webhook
    endpointUrl: {fromEnv: CHANGE_WEBHOOK_URL}
    secret: {fromEnv: CHANGE_WEBHOOK_SECRET}
    headers:
      X-Source: {fromEnv: CHANGE_WEBHOOK_SOURCE}
```

- `endpointUrl` (required, a secret reference).
- Optional: `secret` (signs the body, see [Signature](#signature)), `headers` (a map of secret references) and `meta`.

## Configure the Go module

Add `&webhooknotifier.Notifier` to `ffclient.Config.Notifiers`:

```go
client, err := ffclient.New(&ffclient.Config{
    // ...
    Notifiers: []notifier.Notifier{
        &webhooknotifier.Notifier{
            EndpointURL: "https://example.com/hook",
            Secret:      "Secret",
            Meta: map[string]string{
                "app.name": "my app",
            },
            Headers: map[string][]string{
                "Authorization": {"Bearer auth_token"},
            },
        },
        // ...
    },
})
defer client.Close()
```

| Field | Mandatory | Description |
|---|:---:|---|
| `EndpointURL` | Yes | Full URL that receives the `POST` request (see [Format](#format)). |
| `Secret` | No | Secret used to sign the request body and populate `X-Hub-Signature-256` (see [Signature](#signature)). |
| `Meta` | No | Extra key-value pairs included in the payload's `meta` object. By default, `hostname` is always included. |
| `Headers` | No | Extra HTTP headers sent with each webhook request. |
