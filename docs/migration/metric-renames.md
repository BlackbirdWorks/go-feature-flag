# Prometheus metric renames

Five relay proxy metrics did not follow Prometheus naming conventions (a counter name ends in `_total`).
They have been renamed; update any dashboards, alerts, and recording rules that use them.

| Old name | New name |
|---|---|
| `semaphore_all_flags_evaluations_total_with_flag` | `semaphore_all_flags_evaluations_with_flag_total` |
| `semaphore_flag_update` | `semaphore_flag_update_by_flag_total` |
| `semaphore_flag_delete` | `semaphore_flag_delete_by_flag_total` |
| `semaphore_flag_create` | `semaphore_flag_create_by_flag_total` |
| `semaphore_force_refresh` | `semaphore_force_refresh_total` |

Labels and values are unchanged. All other metrics keep their names.

## Legacy `gofeatureflag_` metrics

The compatibility series exposed by [`exposeLegacyMetrics`](../relay-proxy/configuration.md#exposelegacymetrics) keep
the names GO Feature Flag used, so existing dashboards that read them need no change:
`gofeatureflag_all_flags_evaluations_total_with_flag`, `gofeatureflag_flag_update`, `gofeatureflag_flag_delete`,
`gofeatureflag_flag_create`, and `gofeatureflag_force_refresh`.
