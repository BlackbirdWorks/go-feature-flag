# Flag usage tracking

## What it is

**Flag usage tracking** records which variation each user receives whenever a flag is evaluated, emitting a **feature** event to your configured [exporters](../integrations/exporters/README.md).

- **Understand flag usage**: See which variations users receive to power analytics, dashboards, and experimentation tools.
- **Correlate exposures with outcomes**: Link flag evaluations to downstream business metrics.

## How it works

- **Relay proxy**: Exports a feature event on every evaluation. It also accepts batched events from clients that evaluate locally via `POST /v1/data/collector`.
- **Go module**: Exports feature events directly through its configured exporters.

## Setup

1. **Enable tracking on your flags**: Set `trackEvents: true` in the flag configuration (or omit it; the default is `true`). See [create flags](../configure-flags/create-flags.md).
2. **Configure an exporter**: Add at least one exporter for `feature` events (the default event type). See [export evaluation data](../integrations/exporters/README.md).

No application code changes are required for `feature` events.

## Event format

Each flag-usage event (**feature event**) has the following fields:

| Field | Description |
|---|---|
| `kind` | Always `"feature"` for flag usage events. |
| `contextKind` | `"user"` for a known user, or `"anonymousUser"` for an anonymous context. |
| `userKey` | Targeting key (user or entity identifier) used in the evaluation. |
| `creationDate` | Unix timestamp (seconds) when the flag was evaluated. |
| `key` | Name of the evaluated flag. |
| `variation` | Name of the returned variation. |
| `value` | Resolved value returned to the application. |
| `default` | `true` if evaluation failed and the default value was returned; otherwise `false`. |
| `version` | Version of the flag configuration (if set). |
| `source` | `"SERVER"` when evaluated by the relay proxy; `"PROVIDER_CACHE"` when served from a client-side cache and reported later. |
| `metadata` | Optional static key-value data added by the client for context. |

### Example

A minimal feature event in JSON (for example, as sent by the [webhook exporter](../integrations/exporters/webhook.md)):

```json
{
  "kind": "feature",
  "contextKind": "user",
  "userKey": "94a25909-20d8-40cc-8500-fee99b569345",
  "creationDate": 1618909178,
  "key": "my-feature-flag",
  "variation": "true-variation",
  "value": true,
  "default": false,
  "version": "1.0.0",
  "source": "SERVER"
}
```

To pair flag exposures with custom business events (such as conversions or revenue), use the [OpenFeature Tracking API](tracking-api.md).
