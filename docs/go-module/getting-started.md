# Getting Started

## Installation
```bash
go get github.com/blackbirdworks/semaphore
```

> [!NOTE]
> `modules/core` is a directory of the root module, so `go get` needs no second module. Two `replace` directives of the
> root `go.mod` still do not apply when your module is the consumer, because Go ignores `replace` directives of
> dependencies:
>
> - `github.com/nikunjy/rules => ./rules` (the vendored rules engine, its own module). Your module resolves the
>   upstream `github.com/nikunjy/rules v1.5.0` from the module proxy. It compiles, but it lacks the semver
>   prerelease comparison and the parse-time rejection of invalid rule syntax that this repository relies on: the
>   tests of `modules/core/flag` fail against it (`TestRuleEvaluate` for prerelease versions and
>   `TestRule_validateNikunjyQuery` for malformed parentheses).
> - `github.com/xitongsys/parquet-go => github.com/rudderlabs/parquet-go v0.0.3`. Your module resolves
>   `github.com/xitongsys/parquet-go v1.6.2`, which has the AWS Athena v3 (Trino) incompatibility. It only matters if
>   you import a parquet exporter.
>
> `go get github.com/blackbirdworks/semaphore@<version>` also needs a published version: no release tag exists on
> the repository yet, so use a pseudo-version of a commit of `main` (`@main`) until one is tagged.
> To get the same behaviour as this repository, repeat the replacements in your `go.mod`, with a checkout of this
> repository:
>
> ```
> replace github.com/nikunjy/rules => ../semaphore/rules
> replace github.com/xitongsys/parquet-go => github.com/rudderlabs/parquet-go v0.0.3
> ```
>
> Options to remove the need for the `rules` replacement (not done yet): publish `rules` as its own tagged module and
> require it from the root `go.mod`, or fold it into the root module under a new import path and change the imports.
> For parquet, drop `xitongsys/parquet-go` or move to a maintained fork under its own module path.

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
