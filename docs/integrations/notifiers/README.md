# Notify flag changes

## Overview

A **notifier** alerts external systems when Semaphore loads an updated flag configuration.

- **Team alerts**: Post flag changes to Slack, Discord, or Microsoft Teams.
- **Automation**: Trigger downstream actions with the webhook notifier.
- **Multiple notifiers**: Configure as many notifiers as you need at the same time.

Semaphore triggers configured notifiers whenever a retriever's polling loop detects a configuration change. You can configure notifiers in the **relay proxy** or directly in the **Go module** (each notifier page shows both).

## Author attribution (`metadata.updatedBy`)

If a flag defines `metadata.updatedBy`, Slack, Discord, and Microsoft Teams notifications include the author name (for example, `✏️ Flag "dark-mode" updated by jane`).

Set `metadata.updatedBy` as a plain string in your flag file (for example, from CI using the commit author):

```yaml
dark-mode:
  variations:
    enabled: true
    disabled: false
  defaultRule:
    variation: enabled
  metadata:
    updatedBy: jane
```

Semaphore does not verify this value, so update it on every change or notifications will display the previous editor.

> [!WARNING]
> **Multiple instances**
> Semaphore is stateless, so every running instance sends its own notification. Each notification includes the instance `hostname` so you can tell them apart.

## Available notifiers

- [Slack](slack.md): Send notifications to a Slack channel.
- [Discord](discord.md): Send notifications to a Discord channel.
- [Microsoft Teams](microsoft-teams.md): Send notifications to a Microsoft Teams channel.
- [Webhook](webhook.md): Send JSON payloads to an HTTP endpoint.

## Custom notifier

To build a custom notifier, implement the `notifier.Notifier` interface. `Notify` receives a `notifier.DiffCache` describing the added, updated, and deleted flags:

```go
import "github.com/blackbirdworks/semaphore/notifier"

type Notifier struct{}

func (c *Notifier) Notify(diff notifier.DiffCache) error {
    // ...
    return nil
}
```

Use `notifier.DescribeChanges` to format flag updates the same way built-in notifiers do (one sentence per change with markdown inline code), `notifier.DescribeChangesPlain` for destinations without inline code support, and `notifier.ChangeList` to render the sentences as a list:

```go
for key, update := range diff.Updated {
    changes, err := notifier.DescribeChanges(update.Before, update.After)
    if err != nil {
        // ...
    }
    fmt.Printf("Flag %s updated\n%s\n", key, notifier.ChangeList(changes, "• ", 0))
}
```

> [!NOTE]
> Want to share a custom notifier? Open a [pull request](https://github.com/BlackbirdWorks/semaphore/pulls).

> [!WARNING]
> Custom notifiers plug straight into the Go module, but using one in the relay proxy requires recompiling the proxy binary.
