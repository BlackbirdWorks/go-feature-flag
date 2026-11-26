# Slack

## Overview

The Slack notifier posts a message to a Slack channel whenever Semaphore detects changes in your flag configuration.

Each message lists created, updated, and deleted flags. Updated flags show one sentence per change:

```text
✏️ Flag "dark-mode" updated
• Version set to `2`
• Targeting rule `beta-users` now matches `beta eq true or company eq "acme"` (was `beta eq true`)
• Targeting rule `internal` added: serves `enabled` when `email ew "@example.com"`
• Default rule now serves `disabled` 50%, `enabled` 50% (was `disabled` 90%, `enabled` 10%)
```

Each flag shows at most 50 changes, with a final line counting omitted changes.

## Create a Slack webhook

1. Create an incoming webhook in your Slack workspace (see the [Slack webhook documentation](https://api.slack.com/messaging/webhooks#getting_started)).
2. Copy the webhook URL (for example, `https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX`).

## Configure the relay proxy

Add the Slack notifier to your relay proxy configuration file:

```yaml
# ...
notifiers:
  - kind: slack
    webhookUrl: "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX"
# ...
```

| Field name | Mandatory | Type | Default | Description |
|---|:---:|---|---|---|
| `kind` | Yes | string | **none** | Must be `slack`. Identifies the notifier type. |
| `webhookUrl` | Yes | string | **none** | Full URL of your Slack incoming webhook. |

## Configure the Go module

Add `&slacknotifier.Notifier` to `ffclient.Config.Notifiers`:

```go
client, err := ffclient.New(&ffclient.Config{
    // ...
    Notifiers: []notifier.Notifier{
        &slacknotifier.Notifier{
            SlackWebhookURL: "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX",
        },
        // ...
    },
})
defer client.Close()
```

| Field | Mandatory | Description |
|---|:---:|---|
| `SlackWebhookURL` | Yes | Full URL of your Slack incoming webhook. |
