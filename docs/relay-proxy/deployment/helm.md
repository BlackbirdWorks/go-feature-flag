# Helm

## Overview
The relay proxy can be deployed in Kubernetes using a helm chart.  
Helm is an invaluable tool for configuring and deploying applications to a Kubernetes environment.

The Helm chart lives in this repository under
[`cmd/relayproxy/helm-charts/relay-proxy`](https://github.com/BlackbirdWorks/semaphore/tree/main/cmd/relayproxy/helm-charts/relay-proxy).
The chart is not published to a Helm repository: install it from a local checkout, or from the
`relay-proxy-<version>.tgz` attached to a GitHub Release (available after the first release).
The default image is `ghcr.io/blackbirdworks/semaphore:<chart appVersion>`, published by the release
workflow (available after the first release); until then override `image.repository` and `image.tag`
to use an image you built. The chart is installed in CI with an image built from the same commit.
`relayproxy.extraFiles` ships additional files (for example a flag file for a `file` retriever)
next to the configuration, see `cmd/relayproxy/helm-charts/relay-proxy/ci/smoke-values.yaml`.

## Install Semaphore relay proxy in Kubernetes using Helm
### Prerequisites

- Access to a Kubernetes cluster
- Helm CLI installed on the client machine

### Step 1: Get the chart

```shell
git clone https://github.com/BlackbirdWorks/semaphore.git
cd semaphore
```

### Step 2: Install the Chart

Install the chart from the local directory with your values file.
You can look at the [chart README](https://github.com/BlackbirdWorks/semaphore/blob/main/cmd/relayproxy/helm-charts/relay-proxy/README.md) to know exactly what you can change in the values.yaml file.

```shell
helm install relay-proxy ./cmd/relayproxy/helm-charts/relay-proxy -f values.yaml
```

### Step 3: Verify The Chart Installation

Verify the Helm Chart installation with the Helm list command. For example:

```shell
helm list
```
