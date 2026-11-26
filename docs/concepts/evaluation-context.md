# Evaluation Context

## Overview

An **evaluation context** is a data object passed to Semaphore when evaluating a flag. It describes the user, device, organization, or request environment so [targeting queries](../configure-flags/target-with-flags.md) and rollout rules can decide which variation to serve.

Context attributes typically combine:

- **Static data** (server name, environment, IP address).
- **Dynamic attributes** (user ID, email, role, device type, plan tier).

> [!NOTE]
> **Only attributes you pass are available for targeting.**
> If a rule references an attribute that is missing from the evaluation context, that rule does not match.

## Example context

Suppose **Mike Wazowski** works on the scare floor at Monsters, Inc. with James P. Sullivan and uses your app on an iPad:

- Name, email, and job title (`"Scare assistant"`)
- Employee ID (used as `targetingKey`)
- Location (`"scare floor"`)
- Organization (`"Monsters, Inc."`)
- Device (`"iPad"`) and coworker (`"James P. Sullivan"`)

```json
{
  "targetingKey": "34c7f8ab-6d14-4aa6-a77f-effc6245da6f",
  "firstname": "Mike",
  "lastname": "Wazowski",
  "email": "mike.wazowski@monster.inc",
  "organization": "Monsters, Inc.",
  "jobFunction": "Scare assistant",
  "location": "scare floor",
  "coworker": "James P. Sullivan",
  "device": "iPad"
}
```

## Targeting key

> [!NOTE]
> **When a targeting key is required**
>
> - **Percentage and progressive rollouts** (including rules added by scheduled rollout steps) require a non-empty key for bucketing. By default `targetingKey` is used, or you can configure a [custom bucketing key](../configure-flags/custom-bucketing.md).
> - **Static flags and attribute-only targeting** without bucketing work without a targeting key.

A **targeting key** (`targetingKey`) is a consistent identifier for the evaluation context (such as a user ID, email, session ID, or fingerprint). Hashing it ensures the same entity consistently receives the same variation across requests and sessions.

## Reserved properties

> [!CAUTION]
> The top-level key `semaphore` (and its legacy alias `gofeatureflag`) is reserved for internal Semaphore behavior. Do not use it for custom user attributes.

You can reference these fields in targeting queries, though Semaphore interprets them internally:

| Field | Format | Description |
|---|---|---|
| `semaphore.currentDateTime` | RFC 3339 date string | Overrides the current time for date-based rollout strategies (*experimentation*, *progressive*, and *scheduled*). |
| `semaphore.flagList` | `[]string` | In bulk evaluation mode (client SDKs), evaluates only the listed flags. When empty or unset, all flags are evaluated. |
| `semaphore.exporterMetadata` | `map[string]string\|number\|bool` | Attaches these key/value pairs to exported feature events. |

The canonical reserved key is `semaphore`. The legacy key `gofeatureflag` (sent by GO Feature Flag providers) is still accepted with the same sub-keys (`gofeatureflag.flagList`, ...) and causes the relay proxy to log one warning when seen. If both keys set the same field, `semaphore` wins.
