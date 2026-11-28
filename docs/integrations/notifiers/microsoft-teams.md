# Microsoft Teams

## Overview

The Microsoft Teams notifier posts a message to a Teams channel whenever Semaphore detects changes in your flag configuration.

Each message lists created, updated, and deleted flags. Updated flags show one sentence per change:

```text
✏️ Flag dark-mode updated
  * Version set to 2
  * Targeting rule beta-users now matches beta eq true or company eq "acme" (was beta eq true)
  * Targeting rule internal added: serves enabled when email ew "@example.com"
  * Default rule now serves disabled 50%, enabled 50% (was disabled 90%, enabled 10%)
```

- **Plain text formatting**: Teams cards cannot render inline code, so names and values appear as plain text.
- **Per-flag limit**: Each flag shows at most 50 changes, with a final line counting omitted changes.

## Create a Microsoft Teams webhook

1. Create an incoming webhook in your target Teams channel (see the [Microsoft Teams webhook guide](https://support.microsoft.com/en-us/office/create-incoming-webhooks-with-workflows-for-microsoft-teams-8ae491c7-0394-4861-ba59-055e33f75498)).
2. Copy the webhook URL.

## Configure the relay proxy

Add the notifier to the `notifiers` map of the [bootstrap file](../../relay-proxy/bootstrap-config.md):

```yaml
version: 2
notifiers:
  flag-changes:
    kind: microsoftteams
    webhookUrl: {fromEnv: TEAMS_WEBHOOK_URL}
```

- `kind` is `microsoftteams`; `webhookUrl` (required) is a secret reference: `{fromEnv: NAME}` or `{fromFile: PATH}`. An inline URL is refused.
- Optional: `meta` (a map of string).

## Configure the Go module

Add `&microsoftteamsnotifier.Notifier` to `ffclient.Config.Notifiers`:

```go
client, err := ffclient.New(&ffclient.Config{
    // ...
    Notifiers: []notifier.Notifier{
        &microsoftteamsnotifier.Notifier{
            MicrosoftTeamsWebhookURL: "https://xxx.xxx/...",
        },
    },
})
defer client.Close()
```

| Field | Mandatory | Description |
|---|:---:|---|
| `MicrosoftTeamsWebhookURL` | Yes | Full URL of your Microsoft Teams incoming webhook. |
