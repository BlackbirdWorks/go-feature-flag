# Discord

## Overview

The Discord notifier posts a message to a Discord channel whenever Semaphore detects changes in your flag configuration.

Each message lists created, updated, and deleted flags. Updated flags show one sentence per change:

```text
✏️ Flag "dark-mode" updated
• Version set to `2`
• Targeting rule `beta-users` now matches `beta eq true or company eq "acme"` (was `beta eq true`)
• Targeting rule `internal` added: serves `enabled` when `email ew "@example.com"`
• Default rule now serves `disabled` 50%, `enabled` 50% (was `disabled` 90%, `enabled` 10%)
```

- **Per-flag limit**: Each flag shows at most 50 changes, with a final line counting omitted changes.
- **Message size limit**: Because Discord limits message size, the longest change lists are shortened first when many flags change at once.
- **Flag count limit**: If more than 10 flags change, the message shows the first 9 and asks you to check the logs for the rest.

## Create a Discord webhook

1. Open Discord and go to the target channel.
2. Open the channel's **Settings** menu and go to **Integrations**.
3. Create an incoming webhook (see the [Discord webhook documentation](https://support.discord.com/hc/en-us/articles/228383668-Intro-to-Webhooks)).
4. Copy the webhook URL.

## Configure the relay proxy

Add the notifier to the `notifiers` map of the [bootstrap file](../../relay-proxy/bootstrap-config.md):

```yaml
version: 2
notifiers:
  flag-changes:
    kind: discord
    webhookUrl: {fromEnv: DISCORD_WEBHOOK_URL}
```

- `kind` is `discord`; `webhookUrl` (required) is a secret reference: `{fromEnv: NAME}` or `{fromFile: PATH}`. An inline URL is refused.
- Optional: `meta` (a map of string).

## Configure the Go module

Add `&discordnotifier.Notifier` to `ffclient.Config.Notifiers`:

```go
client, err := ffclient.New(&ffclient.Config{
    // ...
    Notifiers: []notifier.Notifier{
        &discordnotifier.Notifier{
            DiscordWebhookURL: "https://discord.com/api/webhooks/yyyy/xxxxxxx",
        },
    },
})
defer client.Close()
```

| Field | Mandatory | Description |
|---|:---:|---|
| `DiscordWebhookURL` | Yes | Full URL of your Discord incoming webhook. |
