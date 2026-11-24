# Getting Started

## Installation
```bash
go get github.com/blackbirdworks/semaphore
```

> [!NOTE]
> The library depends on the nested module `github.com/blackbirdworks/semaphore/modules/core`. This repository builds
> it from the local `modules/core` directory with a `replace` directive, which Go ignores when your module is the
> consumer. `go get` therefore only works once a version of `modules/core` that the root `go.mod` requires has been
> published (a `modules/core/vX.Y.Z` tag, or a pseudo-version of a commit of `main`). Until then, work from a checkout of
> this repository (`replace github.com/blackbirdworks/semaphore => ../semaphore` and the same for `modules/core`).

## Create a feature flag configuration

Create a new `YAML` file containing your first flag configuration.

```yaml
# 20% of the users will use the variation "my-new-feature"
test-flag:
  variations:
    my-new-feature: true
    my-old-feature: false
  defaultRule:
    percentage:
      my-new-feature: 20
      my-old-feature: 80
```

This flag split the usage of this flag, 20% will use the variation `my-new-feature` and 80% the variation `my-old-feature`.

## SDK Initialisation
First, you need to initialize the `ffclient` with the location of your backend file.
```go linenums="1"
err := ffclient.Init(ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever:      &fileretriever.Retriever{
        Path: "flag-config.yaml",
    },
})
defer ffclient.Close()
```
*This example will load a file from your local computer and will refresh the flags every 3 seconds (if you omit the
PollingInterval, the default value is 60 seconds).*

> [!TIP]
> This is a basic configuration to test locally, in production it is better to use a remote place to store your feature flag configuration file.
>
> Look at the list of available options in the [**Store your feature flag file** page](store_file.md).

## Evaluate your flags
Now you can evaluate your flags anywhere in your code.

```go linenums="1"
user := ffcontext.NewEvaluationContext("user-unique-key")
hasFlag, _ := ffclient.BoolVariation("test-flag", user, false)
if hasFlag {
    // flag "test-flag" is true for the user
} else {
    // flag "test-flag" is false for the user
}
```
