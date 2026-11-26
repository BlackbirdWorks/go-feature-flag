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

Add the Webhook notifier to your relay proxy configuration file:

```yaml
# ...
notifiers:
  - kind: webhook
    endpointUrl: "https://example.com/hook"
# ...
```

| Field name | Mandatory | Type | Default | Description |
|---|:---:|---|---|---|
| `kind` | Yes | string | **none** | Must be `webhook`. Identifies the notifier type. |
| `endpointUrl` | Yes | string | **none** | Full URL that receives the `POST` request (see [Format](#format)). |
| `secret` | No | string | **none** | Secret used to compute the `X-Hub-Signature-256` header (see [Signature](#signature)). |
| `meta` | No | map[string]string | **none** | Extra key-value pairs included in the `meta` object of the payload. |
| `headers` | No | map[string][]string | **none** | Extra HTTP headers sent with each webhook request. |

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
