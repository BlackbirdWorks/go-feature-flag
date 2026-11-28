# Kubernetes ConfigMap

> [!NOTE]
> The relay proxy does not run retrievers: it reads its control plane from a store connection, see the
> [bootstrap file](../../relay-proxy/bootstrap-config.md). This page is about the retriever of the Go module.

## Overview

Load flag configurations from a Kubernetes ConfigMap via the [Kubernetes Go Client](https://github.com/kubernetes/client-go).

## Create a ConfigMap from a flag file

Create a ConfigMap from your `flags.yaml` file with `kubectl`:

```shell
kubectl create configmap semaphore --from-file=flags.yaml
```

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
