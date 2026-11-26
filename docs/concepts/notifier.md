# Notifiers

## Overview

A **notifier** sends alerts when Semaphore detects a change in your flag configuration. Unlike [exporters](exporter.md) (which record flag evaluations), notifiers only react to flag file changes.

## How it works

- When a [retriever](retriever.md) polls and detects a flag change, Semaphore calls every configured notifier.
- Common uses:
  - Posting flag updates to team chat (Slack, Discord, Microsoft Teams).
  - Triggering downstream automation via a webhook.

> [!NOTE]
> **Multiple instances**
> Semaphore is stateless, so running multiple instances sends a notification from each instance. Webhook and chat notifiers include the instance `hostname` so you can tell them apart (the `Log` notifier writes records through your configured logger with the flag `key` only).

## Notifiers vs. Exporters

| Feature | Notifiers | Exporters |
|---|---|---|
| **Purpose** | Notify on config changes | Export flag evaluation events |
| **Trigger** | Retriever detects a flag change | Flag evaluation (for example `BoolValue()`) |
| **Data sent** | Diff of changed flags | Flag key, variation, context, timestamp |
| **Use cases** | Team alerts, webhooks | Analytics, usage monitoring, auditing |

## Supported notifiers

- Slack
- Discord
- Microsoft Teams
- Webhook
- Log

See [Configure notifiers](../integrations/notifiers/README.md) for setup examples.
