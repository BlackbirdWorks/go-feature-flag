# Flag Set

## Overview

A **flag set** is an isolated group of feature flags with its own configuration, API keys, [retrievers](retriever.md), [exporters](exporter.md), and [notifiers](notifier.md).

- **Independent pipelines:** Each flag set configures its own retrievers, exporters, and notifiers.
- **API key routing:** Requests require an API key, which routes evaluation to the matching flag set (a flag set can have multiple API keys).
- **Complete isolation:** Flag sets do not inherit flags or settings from one another.

## When to use flag sets?

- **Team isolation:** Let separate teams (such as frontend and backend) manage their own flags independently—even reusing the same flag key without collisions.
- **Environment separation:** Keep development, staging, and production flags isolated.
- **Multi-tenant applications:** Give each customer or organization isolated flag configs and separate event tracking.

> [!NOTE]
> If you do not need multi-team, multi-environment, or multi-tenant isolation in a single relay proxy, skip flag sets to keep your setup simple.

## How to access a flag set

### Using API keys

When flag sets are configured, authentication is required on all evaluation requests. Configure each OpenFeature provider with an API key belonging to the target flag set.

### Request routing

1. The relay proxy reads the API key from the `X-API-Key` or `Authorization` header.
2. It looks up the flag set associated with that key.
3. The evaluation runs against that flag set's flags and returns the result.

### Flag change streams

WebSocket and SSE flag change streams are scoped to the API key's flag set—a connection receives events only for its own flag set, and the API key must belong to a flag set. Open one connection per flag set to watch multiple sets. See [flag change streams](../relay-proxy/endpoints.md#flag-change-streams).

## Managing flag sets at runtime

The relay proxy watches its configuration file and applies certain flag set changes without a restart:

- **Adding a flag set:** Starts immediately when the file change is detected.
- **Removing a flag set:** Stops the flag set and rejects its API keys.
- **Updating API keys:** Applied live to the existing flag set.

> [!WARNING]
> Modifying any other field on an existing flag set (`retrievers`, `exporters`, `notifiers`, `pollingInterval`, ...) is **not supported at runtime**. The change is ignored with an error log and only takes effect after a relay proxy **restart**.
>
> Runtime add/remove only applies to flag sets with an explicit, unique `name`. Flag sets without a name (or named `default`) receive a generated name at startup and cannot be added or removed at runtime.
