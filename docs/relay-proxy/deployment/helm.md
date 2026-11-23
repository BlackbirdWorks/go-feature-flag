# Helm

## Overview
The relay proxy can be deployed in Kubernetes using a helm chart.  
Helm is an invaluable tool for configuring and deploying applications to a Kubernetes environment.

The Helm chart lives in this repository under
[`cmd/relayproxy/helm-charts/relay-proxy`](https://github.com/BlackbirdWorks/go-feature-flag/tree/main/cmd/relayproxy/helm-charts/relay-proxy).
This repository does not publish the chart to a Helm repository, so install it from a local checkout.
The default image is `ghcr.io/blackbirdworks/go-feature-flag`; override `image.repository` and `image.tag` to
use an image you built and pushed.

## Install Semaphore relay proxy in Kubernetes using Helm
### Prerequisites

- Access to a Kubernetes cluster
- Helm CLI installed on the client machine

### Step 1: Get the chart

```shell
git clone https://github.com/BlackbirdWorks/go-feature-flag.git
cd go-feature-flag
```

### Step 2: Install the Chart

Install the chart from the local directory with your values file.
You can look at the [chart README](https://github.com/BlackbirdWorks/go-feature-flag/blob/main/cmd/relayproxy/helm-charts/relay-proxy/README.md) to know exactly what you can change in the values.yaml file.

```shell
helm install relay-proxy ./cmd/relayproxy/helm-charts/relay-proxy -f values.yaml
```

### Step 3: Verify The Chart Installation

Verify the Helm Chart installation with the Helm list command. For example:

```shell
helm list
```
