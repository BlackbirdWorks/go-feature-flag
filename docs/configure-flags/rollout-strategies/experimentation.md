# Experimentation rollout

## Overview

An **experimentation rollout** limits a flag to a specific time window so you can run a time-boxed test before rolling out broadly.

## Define an experimentation rollout

Set `experimentation` with `start` and `end` timestamps on the flag.

- Between `start` and `end`, the flag evaluates normally.
- Outside that window, Semaphore considers the flag inactive and returns the caller's default value.

## Format

> [!NOTE]
> Use the native date/time format of your flag file format (such as RFC 3339 timestamps in YAML).

| Field | Description |
|---|---|
| `start` | Timestamp when the flag becomes active. |
| `end` | Timestamp when the flag stops being served. |

## Example

```yaml
experimentation-flag:
  variations:
    enabled: true
    disabled: false
  defaultRule:
    percentage:
      enabled: 50
      disabled: 50
  experimentation:
    start: 2021-03-20T00:00:00.1-05:00
    end: 2021-03-21T00:00:00.1-05:00
```

## A/B testing

Combine `experimentation` with [data collection](../../go-module/data_collection.md) (or an exporter in the relay proxy) to track which variation each user received during the test window.

To estimate how long an experiment should run, you can use VWO's [A/B test duration calculator](https://vwo.com/tools/ab-test-duration-calculator/).
