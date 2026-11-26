# Progressive rollout

## Overview

A **progressive rollout** gradually shifts traffic from an initial variation to a target variation automatically over a configured time window.

## Define a progressive rollout

Set `progressiveRollout` with `initial` and `end` states:

- **Before `initial.date`:** Returns the `initial` variation.
- **Between `initial.date` and `end.date`:** Gradually ramps traffic from `initial` to `end` (by default from `0%` to `100%`).
- **After `end.date`:** Returns the `end` variation (or holds at `end.percentage`).

## Format

> [!NOTE]
> Use the native date/time format of your flag file format.

| Field | Description |
|---|---|
| `initial` | Starting state: `variation` (starting variation) and `date` (rollout start time). |
| `end` | Target state: `variation` (target variation) and `date` (rollout completion time). |
| `percentage` *(optional)* | Starting (`initial`) and ending (`end`) percentage ramp bounds.<br/>**Default: `initial` = `0` and `end` = `100`**. |

## Example

Gradually shifts traffic from `variationA` (`0%`) to `variationB` (`100%`) between `2024-01-01T05:00:00.100Z` and `2024-01-05T05:00:00.100Z`:

```yaml
progressive-flag:
  variations:
    variationA: A
    variationB: B
  defaultRule:
    progressiveRollout:
      initial:
        variation: variationA
        date: 2024-01-01T05:00:00.100Z
      end:
        variation: variationB
        date: 2024-01-05T05:00:00.100Z
```

## Advanced: Custom `percentage` bounds

Use `percentage` on `initial` and `end` to start at a non-zero split or cap the rollout below `100%`:

```yaml
    progressiveRollout:
      initial:
        variation: variationA
        percentage: 20
        date: 2024-01-01T05:00:00.100Z
      end:
        variation: variationB
        percentage: 80
        date: 2024-01-05T05:00:00.100Z
```

- Before `initial.date`, all users receive **`variationA`**.
- At `initial.date`, traffic starts at **20% `variationB`** and **80% `variationA`**.
- Between `initial.date` and `end.date`, **`variationB`** ramps smoothly from **20% to 80%**.
- At and after `end.date`, traffic stays at **80% `variationB`** and **20% `variationA`**.
