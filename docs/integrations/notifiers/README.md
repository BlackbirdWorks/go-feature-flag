# Notify flag changes

## Overview
In Semaphore, if you want to be informed when a flag has changed, you can configure a notifier.

A notifier will send one notification to the targeted system to inform them that a new flag configuration has been loaded.

## About notifiers
Notifiers are useful for a variety of purposes, such as:
- **Information**: Notifiers can send to your team chat a notification that a feature flag has changed.
- **Reacting to flag changes**: Using the webhook notifier, you can trigger any action you want in your system to react to flag changes.

When the Semaphore client detects a change in the configuration through the retriever's polling mechanism, it triggers the configured notifiers.

> [!NOTE]
> Semaphore can handle more than one notifier at a time.

If a flag has a `metadata.updatedBy` value (set it to the name of the person who last changed the flag),
the Slack, Discord and Microsoft Teams notifications name who created or updated the flag, for example `✏️ Flag "dark-mode" updated by jane`.

If you edit your flag file yourself, set it as a plain string, for example from your CI with the commit author:

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

Semaphore shows the value as it is in the file and does not verify it, so update it with every change or the notification names the previous editor.

> [!WARNING]
> **Multiple instances**
> Semaphore is stateless, so running multiple instances sends a notification from each instance. Every notification includes the instance `hostname` so you can tell them apart.

You have 2 different ways to specify a notifier depending on if you are using the **Go module** or the **Relay Proxy**.
In all the details pages of the notifiers below you will have an example for both usages (relay proxy and Go module).

## Available Notifiers
Below is the full list of notifiers that are available in Semaphore.

- [Slack](slack.md): Send notifications to a Slack channel.
- [Discord](discord.md): Send notifications to a Discord channel.
- [Microsoft Teams](microsoft-teams.md): Send notifications to a Microsoft Teams channel.
- [Webhook](webhook.md): Send notifications to a Webhook in a specific format.

## Custom notifier
If you have a specific use case that is not covered by the built-in notifiers, you can create your own custom notifier.

To create a custom notifier you must have a struct that implements the notifier.Notifier interface.

In param you will receive a `notifier.DiffCache` struct that will tell you what has changed in your flag configuration.

```go
import (
    ffclient "github.com/blackbirdworks/semaphore"
    "github.com/blackbirdworks/semaphore/notifier"
    "sync"
)

type Notifier struct{}
func (c *Notifier) Notify(diff notifier.DiffCache) error {
    // ...
    // do whatever you want here
}
```

To describe an updated flag the same way the built-in notifiers do, use `notifier.DescribeChanges`.
It returns one sentence per change, with names and values formatted as markdown inline code.
Use `notifier.DescribeChangesPlain` if your destination cannot display inline code, and
`notifier.ChangeList` to render the sentences as a list.

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
> If you think your custom notifier could be useful for others, feel free to open a [pull request](https://github.com/BlackbirdWorks/semaphore/pulls) to add it to the list of available notifiers.

> [!WARNING]
> If you are using a custom notifier, it is easy to use it for the Go module, but if you want to use it in the relay proxy you will have to recompile it yourself.
