# Notifier

## Overview
In Semaphore, a **notifier** is a component that informs you about changes in the feature flag configuration.

It's distinct from [exporters](exporter.md), which handle the results of flag evaluations. Notifiers focus on changes to the configuration itself.

## About notifiers

When the Semaphore client detects a change in the configuration through the retriever's polling mechanism, it triggers the configured notifiers.
The notifier then sends a notification to the specified destination.

Notifiers are useful for a variety of purposes, such as:
- **Information**: Notifiers can send to your team chat a notification that a feature flag has changed.
- **Reacting to flag changes**: Using the webhook notifier, you can trigger any action you want in your system to react to flag changes.

> [!NOTE]
> ****Limitation if you are running multiple Semaphore****
> By nature, Semaphore is 100% stateless, so if you run multiple instance of Semaphore, you will receive notification for changes from each instance.
> In each notification you will have the `hostname` of the instance, it can be useful if you have multiple instances running.

## Key Differences Between Notifiers and Exporters

| Feature       | Notifiers                                   | Exporters                                         |
|---------------|---------------------------------------------|---------------------------------------------------|
| **Purpose**   | 	Notify about configuration changes         | 	Export flag evaluation results.                  |
| **Trigger**   | 	Configuration update detected by retriever | 	Flag evaluation _(e.g., calling `BoolValue()`)_. |
| **Data Sent** | 	Information about the configuration change | 	Flag name, variation, user context, timestamp.   |
| **Use Cases** | 	Informing about configuration changes      | 	Analytics, monitoring flag usage, debugging.     |

## Supported notifiers

- Slack
- Discord
- Microsoft Teams
- Webhook
- Log

[Check how to configure the notifiers](../integrations/notifiers/README.md)
