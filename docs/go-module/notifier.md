# Notify flag changes

Configure a [**notifier**](https://pkg.go.dev/github.com/blackbirdworks/semaphore#NotifierConfig) in `ffclient.Config{}` to send a notification whenever a new flag configuration is loaded.

> [!NOTE]
> `semaphore` supports configuring multiple notifiers at the same time.

## Available notifiers

- [Slack](../integrations/notifiers/slack.md)
- [Discord](../integrations/notifiers/discord.md)
- [Microsoft Teams](../integrations/notifiers/microsoft-teams.md)
- [Webhook](../integrations/notifiers/webhook.md)
- Log

> [!NOTE]
> Each notifier page documents its configuration options.
