# Flag usage tracking

## What it is

**Flag usage tracking** records which flag variation each user received whenever a flag is evaluated.  
For each evaluation, a **feature** event is recorded and sent to the exporters you have configured.

All flag usage events are sent to the [exporters](../integrations/exporters/README.md) you have configured.

## Why use it

- **Understand how flags are used**: See who saw what variation, power analytics and dashboards, and feed data into A/B or experimentation tools.
- **Correlate exposures with outcomes**: Essential when you need to link flag evaluations to business metrics or downstream events.

## How it works

Every evaluation performed by the relay-proxy produces an event that the relay-proxy exports to your configured exporters.
If you use the Go module, the module itself exports the events.

The relay-proxy also accepts batched events from clients that evaluate flags locally and report their usage afterwards, through the `POST /v1/data/collector` endpoint.

### Summary

Flag usage tracking means: _every time someone gets a flag value, record that fact and send it to your chosen destinations._

## What you need to do

1. **Turn on tracking for the flags you care about**: Set `trackEvents: true` in the flag configuration of your feature flag(or omit it; the default is `true`). See [configure your flags](../configure-flags/create-flags.md) for the flag format.
2. **Configure at least one exporter for evaluation data**: Add a data exporter for feature events (the default event type). See [export evaluation data](../integrations/exporters/README.md) for available exporters and how to configure them for the relay-proxy or Go module.

No code changes are required beyond configuration for the default "feature" event type.

## Event format

Each flag-usage event (also called a **feature event**) has the following structure when sent to your exporters:

| Field            | Description                                                                                                                                           |
| ---------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| **kind**         | Always `"feature"` for flag usage events.                                                                                                             |
| **contextKind**  | `"user"` for a known user, or `"anonymousUser"` for an anonymous context.                                                                             |
| **userKey**      | The targeting key (user or entity identifier) used in the evaluation.                                                                                 |
| **creationDate** | Unix timestamp (seconds) when the flag was evaluated.                                                                                                 |
| **key**          | The flag name that was evaluated.                                                                                                               |
| **variation**    | The variation name returned.                                                                                                                          |
| **value**        | The resolved value returned to the application.                                                                                                       |
| **default**      | `true` if the evaluation failed and the default value was returned; otherwise `false`.                                                                |
| **version**      | Version of the flag configuration (if set).                                                                                                           |
| **source**       | `"SERVER"` when the evaluation was done in the relay-proxy; `"PROVIDER_CACHE"` when it was served from a client-side cache and later reported to the relay. |
| **metadata**     | Optional static key-value data added by the client for context.                                                                                     |

### Example

A minimal feature event in JSON (as sent for example by the [webhook exporter](../integrations/exporters/webhook.md)) looks like:

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

To combine flag usage with custom business events (e.g. conversions or revenue), use the [OpenFeature Tracking API](tracking-api.md).
