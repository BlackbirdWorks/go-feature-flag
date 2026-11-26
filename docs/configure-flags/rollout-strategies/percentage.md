# Percentage rollout

## Overview

A **percentage rollout** (also called a **canary release**) serves a feature to a fixed percentage of users instead of everyone at once.

## Define a percentage rollout

Configure `percentage` in `defaultRule` or any `targeting` rule as a map of **variation name** to **percentage weight**:

> [!NOTE]
> Percentages do not have to sum to 100—Semaphore normalizes them as fractions of the total weight.
> Each percentage must be a finite, non-negative number (negative, `NaN`, or infinite values are rejected).
>
> *Example: `variation1: 10` and `variation2: 50` splits traffic as `10/60` and `50/60`.*

## How percentage rollout works

Semaphore uses deterministic hashing so the same user consistently gets the same variation across servers and relay proxy restarts.

### Hash computation

For each evaluation, Semaphore hashes the **targeting key** (or custom `bucketingKey`) and the **flag name**:

```
hash = hash(targetingKey + flagName) % maxPercentage
```

This produces a value between `0` and `maxPercentage` (the sum of all percentage weights).

### Bucket assignment

Variations in `percentage` are sorted in **reverse alphabetical order** to build contiguous buckets.

For example:

```yaml
percentage:
  varC: 20
  varB: 30
  varA: 50
```

After sorting in reverse alphabetical order, buckets are assigned as:

- **0 to 20:** `varC`
- **20 to 50:** `varB`
- **50 to 100:** `varA`

### Impact of changing percentages

- **Two variations:** Adjusting weights cleanly shifts traffic from one variation to the other.
- **More than two variations:** Changing weights recalculates all bucket boundaries, which can reassign some users to different variations.

For example, changing the weights above to:

```yaml
percentage:
  varC: 30
  varB: 30
  varA: 40
```

Shifts the bucket boundaries to:

- **0 to 30:** `varC`
- **30 to 60:** `varB`
- **60 to 100:** `varA`

> [!NOTE]
> Deterministic hashing guarantees consistent assignment for a fixed configuration, but editing percentages shifts bucket boundaries and may reassign some users.

## Examples

### In `defaultRule`

Serves `disabled` to `99%` of users and `enabled` to `1%`:

```yaml
percentage-flag-1:
  variations:
    disabled: false
    enabled: true
  defaultRule:
    percentage:
      disabled: 99
      enabled: 1
```

### In a `targeting` rule

Splits `admin` users 50/50 between `disabled` and `enabled`, while non-admin users receive `disabled`:

```yaml
percentage-flag-1:
  variations:
    disabled: false
    enabled: true
  targeting:
    - query: admin eq true
      percentage:
        disabled: 50
        enabled: 50
  defaultRule:
    variation: disabled
```
