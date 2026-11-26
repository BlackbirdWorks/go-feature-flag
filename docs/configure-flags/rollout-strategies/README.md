# Rollout Strategies

Semaphore supports four rollout strategies you can use in `defaultRule`, `targeting` rules, or at the flag level.

## Available strategies

| Strategy | When to use | How it works |
|---|---|---|
| **[Percentage](percentage.md)** | Fixed A/B/n or canary traffic splits | Assigns users deterministically using percentage weights and a hash of the bucketing key. Weights stay fixed until you edit the config. |
| **[Progressive](progressive.md)** | Gradual automated ramp-up | Increases traffic from an initial percentage to a target percentage between a start and end time. Users stick to the new variation once promoted. |
| **[Experimentation](experimentation.md)** | Time-boxed A/B tests | Evaluates the flag only between `start` and `end` timestamps; outside that window, callers receive their default value. |
| **[Scheduled](scheduled.md)** | Multi-stage launches | Applies ordered flag config patches at specific dates and times (for example, adding targeting rules or changing percentages). |
