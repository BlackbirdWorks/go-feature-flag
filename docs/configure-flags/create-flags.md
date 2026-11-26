# Create Flags

## Overview

In Semaphore, flags are defined as key/value entries in `YAML`, `JSON`, or `TOML` files and loaded by a [**retriever**](../concepts/retriever.md).

> [!NOTE]
> **OpenFeature definition**
> A feature flag lets an application select alternative code paths at runtime based on rules and context, without redeploying.

## Create your first flag

1. Create a `YAML`, `JSON`, or `TOML` file for your flags.
2. Pick a unique flag key (for example, `display-banner`).
3. Define its `variations` and a `defaultRule`:

```yaml
display-banner:
  variations:
    enabled: true
    disabled: false
  defaultRule:
    variation: disabled
```

4. Store the file in any supported [retriever](../integrations/retrievers/README.md) and point Semaphore at it.

### Supported types

Flags can return **booleans**, **strings**, **integers**, **floats**, **JSON arrays**, or **JSON objects**.

> [!TIP]
> **Keep variation types consistent**
> Do not change a flag's value type after rollout—SDK callers expect a stable type.

### Multi-variant flags

Define as many variations as needed (for example, A/B/n tests or plan tiers). Every variation in a flag must share the same type:

```yaml
scream-level-feature:
  variations:
    low: 'whisper'
    medium: 'talk'
    high: 'scream'
  defaultRule:
    variation: low
```

> [!CAUTION]
> All variations in a single flag must have the same type, or the flag is rejected as invalid.

### Percentage splits

Split traffic across variations using `percentage` weights:

```yaml
scream-level-feature:
  variations:
    low: 'whisper'
    medium: 'talk'
    high: 'scream'
  defaultRule:
    percentage:
      low: 10
      medium: 50
      high: 40
```

> [!NOTE]
> **Deterministic bucketing**
> Semaphore hashes the flag name with the `targetingKey` (or `bucketingKey`), so the same user consistently gets the same variation.

## Format reference

| Field | Required | Default | Description |
|---|---|---|---|
| `<flag-key>` | Yes | — | Unique flag identifier (top-level map key). |
| `variations` | Yes | — | Map of variation names to typed values (`bool`, `string`, `int`, `float`, `array`, or `object`). |
| `defaultRule` | Yes | — | Rule applied when no `targeting` rule matches. See [Targeting rules](target-with-flags.md). |
| `targeting` | No | `[]` | Ordered list of targeting rules. See [Targeting rules](target-with-flags.md). |
| `bucketingKey` | No | `targetingKey` | Evaluation context attribute hashed instead of `targetingKey`. If missing from the context, the flag falls back to default. |
| `trackEvents` | No | `true` | Set to `false` to skip exporting evaluation events for this flag. |
| `disable` | No | `false` | Set to `true` to disable the flag and return the caller's default value. |
| `version` | No | `""` | Optional flag version included in notifications and exported events. |
| `metadata` | No | `{}` | Arbitrary key/value metadata (for example, links, owner, or `updatedBy`). |
| `scheduledRollout` | No | `[]` | Time-based patches applied to the flag. See [Scheduled rollout](rollout-strategies/scheduled.md). |
| `experimentation` | No | — | Start/end window during which the flag is active. See [Experimentation](rollout-strategies/experimentation.md). |

Example variation value types:

```yaml
example-types:
  variations:
    str: "test"
    flag: true
    count: 1000
    ratio: 1000.23
    items:
      - item1
      - item2
    config:
      item1: 123
      item2: "this is a string"
      item3: false
```

## Advanced options

- [Targeting rules](target-with-flags.md)
- [Rollout strategies](rollout-strategies/README.md)
- [Custom bucketing](custom-bucketing.md)

---

## Example flag lifecycle

Suppose we want to roll out a new `scream-level-feature` starting from `"whisper"`:

#### 1. Create the flag

```yaml
scream-level-feature:
  variations:
    low: 'whisper'
    medium: 'talk'
    high: 'scream'
  defaultRule:
    variation: low
```

#### 2. Deploy code

Deploy code that evaluates `scream-level-feature`. Behavior stays identical (`"whisper"`).

#### 3. Target internal testers

Target a specific `targetingKey` with `high` while everyone else keeps `low`:

```yaml
scream-level-feature:
  variations:
    low: 'whisper'
    medium: 'talk'
    high: 'scream'
  targeting:
    - query: targetingKey eq "aae1cb41-c3cb-4753-a117-031ddc958e81"
      variation: high
  defaultRule:
    variation: low
```

#### 4. Run a percentage split

Split traffic across all variations to compare results:

```yaml
scream-level-feature:
  variations:
    low: 'whisper'
    medium: 'talk'
    high: 'scream'
  defaultRule:
    percentage:
      low: 34
      medium: 33
      high: 33
```

#### 5. Promote the winner

Set `high` as the default once it wins:

```yaml
scream-level-feature:
  variations:
    low: 'whisper'
    medium: 'talk'
    high: 'scream'
  defaultRule:
    variation: high
```

#### 6. Clean up

Once the feature is permanent, remove the flag check from code and delete the entry from the flag file.
