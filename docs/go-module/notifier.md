# Notify flag changes
If you want to be informed when a flag has changed, you can configure a [**notifier**](https://pkg.go.dev/github.com/blackbirdworks/semaphore#NotifierConfig).

A notifier will send one notification to the targeted system to inform them that a new flag configuration has been loaded.

> [!NOTE]
> `semaphore` can handle more than one notifier at a time.

## Available notifiers
- [Slack](../integrations/notifiers/slack.md)
- [Discord](../integrations/notifiers/discord.md)
- [Microsoft Teams](../integrations/notifiers/microsoft-teams.md)
- [Webhook](../integrations/notifiers/webhook.md)
- Log

> [!NOTE]
> In each notifier documentation, you will find the configuration needed to set up the notifier.
