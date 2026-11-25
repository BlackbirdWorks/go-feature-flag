# Kubernetes ConfigMap

## Overview
Loads the configuration from a Kubernetes ConfigMap. This retriever is useful when you are using Kubernetes and want to use ConfigMaps to store your configuration files.

The **Kubernetes Retriever** will access flags in a Kubernetes ConfigMap via the [Kubernetes Go Client](https://github.com/kubernetes/client-go).

## Add a flag configuration file as ConfigMap
If you have a flag configuration file, you can create a ConfigMap with the content of the file.
The following command will create a ConfigMap with the content of the `flags.yaml` file:
```shell
kubectl create configmap semaphore --from-file=flags.yaml
```
## Configure the relay proxy

To configure your relay proxy to use the Kubernetes ConfigMap retriever, you need to add the following
configuration to your relay proxy configuration file:

> [!NOTE]
> Relay proxy is only supporting the configmaps while running inside the kubernetes cluster.

```yaml
# ...
retrievers:
  - kind: configmap
    namespace: default
    configmap: my-configmap
    key: my-flags.yml
# ...
```

| Field name  |   Mandatory   | Type   | Default  | Description                                                                                                 |
|-------------|:-------------:|--------|----------|-------------------------------------------------------------------------------------------------------------|
| `kind`      | Yes | string | **none** | Value should be **`configmap`**.<br/>_This field is mandatory and describes which retriever you are using._ |
| `namespace` | Yes | string | **none** | This is the name of the namespace where your **configmap** is located _(ex: `default`)_.                    |
| `configmap` | Yes | string | **none** | Name of the **configmap** we should read  _(ex: `feature-flag`)_.                                           |
| `key`       | Yes | string | **none** | Name of the `key` in the **configmap** which contains the flag.                                             |

## Configure the GO Module
To configure your GO module to use the Kubernetes ConfigMap retriever, you need to add the following
configuration to your `ffclient.Config{}` object:

```go
import (
    restclient "k8s.io/client-go/rest"
)
// ...
config, _ := restclient.InClusterConfig()
client, err = ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &k8sretriever.Retriever{
        Path: "file-example.yaml",
        Namespace:      "default"
        ConfigMapName: "my-configmap"
        Key:    "my-flags.yml"
        ClientConfig: &config
    },
})
defer client.Close()
```

| Field               |   Mandatory   | Description                                        |
|---------------------|:-------------:|----------------------------------------------------|
| **`Namespace`**     | Yes | The namespace of the ConfigMap.                    |
| **`ConfigMapName`** | Yes | The name of the ConfigMap.                         |
| **`Key`**           | Yes | The key within the ConfigMap storing the flags.    |
| **`ClientConfig`**  | Yes | The configuration object for the Kubernetes client |
