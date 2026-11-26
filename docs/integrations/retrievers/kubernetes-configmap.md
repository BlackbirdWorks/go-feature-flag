# Kubernetes ConfigMap

## Overview

Load flag configurations from a Kubernetes ConfigMap via the [Kubernetes Go Client](https://github.com/kubernetes/client-go).

## Create a ConfigMap from a flag file

Create a ConfigMap from your `flags.yaml` file with `kubectl`:

```shell
kubectl create configmap semaphore --from-file=flags.yaml
```

## Configure the relay proxy

> [!NOTE]
> The relay proxy only supports ConfigMaps when running inside a Kubernetes cluster.

Add this configuration to your relay proxy config file:

```yaml
# ...
retrievers:
  - kind: configmap
    namespace: default
    configmap: my-configmap
    key: my-flags.yml
# ...
```

| Field name  | Mandatory | Type   | Default  | Description |
|-------------|:---------:|--------|----------|-------------|
| `kind`      | Yes       | string | **none** | Value should be **`configmap`**.<br/>_Mandatory field identifying the retriever._ |
| `namespace` | Yes       | string | **none** | Namespace where your **ConfigMap** is located _(ex: `default`)_. |
| `configmap` | Yes       | string | **none** | Name of the **ConfigMap** to read _(ex: `feature-flag`)_. |
| `key`       | Yes       | string | **none** | Key inside the **ConfigMap** holding the flags. |

## Configure the Go module

Add the retriever to `ffclient.Config{}`:

```go
import (
    restclient "k8s.io/client-go/rest"
)
// ...
k8sConfig, _ := restclient.InClusterConfig()
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &k8sretriever.Retriever{
        Namespace:     "default",
        ConfigMapName: "my-configmap",
        Key:           "my-flags.yml",
        ClientConfig:  *k8sConfig,
    },
})
defer client.Close()
```

| Field               | Mandatory | Description |
|---------------------|:---------:|-------------|
| **`Namespace`**     | Yes       | Namespace of the ConfigMap. |
| **`ConfigMapName`** | Yes       | Name of the ConfigMap. |
| **`Key`**           | Yes       | Key within the ConfigMap storing the flags. |
| **`ClientConfig`**  | Yes       | Configuration object (`restclient.Config`) for the Kubernetes client. |
