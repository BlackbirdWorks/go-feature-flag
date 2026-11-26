# Scheduled rollout

## Overview

A **scheduled rollout** applies staged updates to a flag configuration automatically at specified timestamps—for example, enabling a feature for internal users tomorrow and for beta users four days later.

## Define a scheduled rollout

Add `scheduledRollout` as an array of **steps**. Each step includes a `date` and any flag fields to update when that timestamp is reached.

## Format

> [!NOTE]
> Scheduled steps can modify any field supported by the flag format. Because the flag file itself is not edited, scheduled transitions do not trigger notifiers.

| Field | Description |
|---|---|
| `steps` | Each step requires `date` (**steps without `date` are skipped**). Any other fields in the step override the corresponding fields on the flag when `date` is reached. |

## Example

This flag updates twice automatically:

1. On `2020-04-10T00:00:00.1+02:00`, adds targeting rule `rule1` to serve `variationB` to `beta` users.
2. On `2022-05-12T15:36:00.1+02:00`, updates `rule1`'s `query` to target non-beta users (`beta eq "false"`).

```yaml
scheduled-flag:
  variations:
    variationA: A
    variationB: B
  defaultRule:
    name: defaultRule
    percentage:
      variationA: 100
      variationB: 0
  scheduledRollout:
    - date: 2020-04-10T00:00:00.1+02:00
      targeting:
        - name: rule1
          query: beta eq "true"
          percentage:
            variationA: 0
            variationB: 100

    - date: 2022-05-12T15:36:00.1+02:00
      targeting:
        - name: rule1
          query: beta eq "false"
```
