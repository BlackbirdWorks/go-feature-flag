# Getting Started

## Installation

```bash
go get github.com/blackbirdworks/semaphore
```

> [!NOTE]
> `modules/core` and `rules` are directories of the root module, so `go get` needs no second module. One `replace`
> directive in the root `go.mod` still does not apply when your module is the consumer, because Go ignores `replace`
> directives of dependencies:
>
> - `github.com/xitongsys/parquet-go => github.com/rudderlabs/parquet-go v0.0.3`. Your module resolves
>   `github.com/xitongsys/parquet-go v1.6.2`, which has the AWS Athena v3 (Trino) incompatibility. It only matters if
>   you import a parquet exporter.
>
> `go get github.com/blackbirdworks/semaphore@<version>` needs a published version: no release tag exists on
> the repository yet, so use a pseudo-version of a commit of `main` (`@main`) until one is tagged.
> To get the same behaviour as this repository, repeat the replacement in your `go.mod`:
>
> ```
> replace github.com/xitongsys/parquet-go => github.com/rudderlabs/parquet-go v0.0.3
> ```
>
> Or drop `xitongsys/parquet-go` or move to a maintained fork under its own module path.

## Create a feature flag configuration

Create a `YAML` file with your first flag:

```yaml
# 20% of users will receive the "my-new-feature" variation
test-flag:
  variations:
    my-new-feature: true
    my-old-feature: false
  defaultRule:
    percentage:
      my-new-feature: 20
      my-old-feature: 80
```

This splits traffic: 20% receive `my-new-feature` and 80% receive `my-old-feature`.

## SDK initialization

Initialize `ffclient` with the location of your flag file:

```go
client, err := ffclient.New(&ffclient.Config{
    PollingInterval: 3 * time.Second,
    Retriever: &fileretriever.Retriever{
        Path: "flag-config.yaml",
    },
})
if err != nil {
    log.Fatal(err)
}
defer client.Close()
```

This example loads a local file and refreshes flags every 3 seconds (if `PollingInterval` is omitted, the default is 60 seconds).

> [!TIP]
> Local files work well for testing; in production, use a remote retriever. See [Store your feature flag file](store_file.md) for all options.

## Evaluate your flags

Evaluate flags anywhere in your application:

```go
user := ffcontext.NewEvaluationContext("user-unique-key")
hasFlag, _ := client.BoolVariation("test-flag", user, false)
if hasFlag {
    // flag "test-flag" is true for the user
} else {
    // flag "test-flag" is false for the user
}
```
