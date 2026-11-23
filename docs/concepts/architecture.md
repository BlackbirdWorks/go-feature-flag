# Architecture

## Overview
Semaphore is a feature flagging system that allows you to manage feature flags in your application.  
The architecture is really simple and efficient with the goal to be easy to use and let you experiment with feature flags in your application as fast as possible.

In most cases you have 1 microservice running the `relay-proxy` and multiple applications running OpenFeature SDKs, nothing else.

> [!NOTE]
> Semaphore can also be run without the `relay-proxy` if you only use the GO Module.
>
> The relay-proxy is an API layer on top of the GO Module, so it has the same logic inside.

## Architecture concepts

### OpenFeature SDKs and OFREP
Semaphore is compatible with [OpenFeature](https://openfeature.dev). Applications use the standard OpenFeature SDK of their language together with a generic
[OFREP](../openfeature/ofrep.md) (OpenFeature Remote Evaluation Protocol) provider, which talks to the relay proxy over HTTP.
This repository does not ship language-specific providers.

This is what developers will use to interact with Semaphore in their application.

### Relay-Proxy
The relay-proxy is the only component you have to run in your infrastructure to use Semaphore with the different SDKs.  
This is a standalone GO binary that you can run in your infrastructure to expose the Semaphore API to your applications.

> [!NOTE]
> Want to know more about the relay-proxy? [Check the relay-proxy documentation](../relay-proxy/README.md).

The relay-proxy is designed to be simple, lightweight, and stateless. It operates without the need for any databases or complex systems, as it loads feature flag configuration files from a specified location and stores them in memory.

### Internal concepts
**You have 3 main concepts in Semaphore architecture:**
- [retrievers](retriever.md): This is the component in charge of fetching the feature flag configuration.
- [notifiers](notifier.md): This is the component in charge of notifying external systems that a flag configuration has changed.
- [exporters](exporter.md): This is the component in charge of exporting the flag evaluation results for analytics or monitoring.

Those 3 concepts are the core of Semaphore and allows you to integrate Semaphore in your current ecosystem with a minimum of effort.  
Every component is designed to be as lightweight as possible and to be easy to configure.

## Technologies

- Semaphore is written in `golang`.
- The `retrievers`, `notifiers`, and `exporters` are using official libraries to interact with external systems.
