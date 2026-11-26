# Performing flag evaluations

## Users

Feature flag targeting and rollouts depend on the evaluation context passed to your **Variation** calls. Use `ffcontext.NewEvaluationContext` or `ffcontext.NewEvaluationContextBuilder` to build it:

```go
// User with only a key
user1 := ffcontext.NewEvaluationContext("user1-key")

// User with a key plus custom attributes
user2 := ffcontext.NewEvaluationContextBuilder("user2-key").
    AddCustom("firstname", "John").
    AddCustom("lastname", "Doe").
    AddCustom("email", "john.doe@example.com").
    Build()
```

- The user's key provides a stable identifier for bucketing.
- It is required for percentage or progressive rollouts (including scheduled steps that introduce them), unless a [custom bucketing key](../configure-flags/custom-bucketing.md) supplies the value.
- Static flags and attribute-only targeting rules can be evaluated without a user key.
- Use a unique, consistent key per user (such as a primary key, email address, or hash—**a hash is recommended**).
- Other attributes are optional unless referenced by targeting rules or custom bucketing.

> [!NOTE]
> Custom attributes let you target users in rules using any data you attach to the context.

## Anonymous users

Mark anonymous users with `NewAnonymousEvaluationContext` or `.Anonymous(true)`:

```go
// Anonymous user with only a key
user1 := ffcontext.NewAnonymousEvaluationContext("user1-key")

// Anonymous user with custom attributes
user2 := ffcontext.NewEvaluationContextBuilder("user2-key").
    Anonymous(true).
    AddCustom("firstname", "John").
    AddCustom("lastname", "Doe").
    AddCustom("email", "john.doe@example.com").
    Build()
```

Generate a unique key for anonymous users (session IDs or UUIDs work well). Anonymous users behave like regular users and allow rules to target anonymous traffic specifically.

## Variation

Call the typed variation method matching your flag's return type:

- [`BoolVariation`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#BoolVariation)
- [`IntVariation`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#IntVariation)
- [`Float64Variation`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#Float64Variation)
- [`StringVariation`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#StringVariation)
- [`JSONArrayVariation`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#JSONArrayVariation)
- [`JSONVariation`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#JSONVariation)

```go
result, _ := client.BoolVariation("your.feature.key", user, false)
// result is true or false depending on the flag evaluation
```

Variation methods take the **flag key**, **user**, and **default value**. The default value is returned if an error occurs (`ffclient` not initialized, wrong variation type, missing flag, etc.). In the example above, if `your.feature.key` does not exist, `result` is `false`—so you always get a usable value.

## Variation details

Use the `*VariationDetails` methods to inspect evaluation metadata alongside the value:

- [`BoolVariationDetails`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#BoolVariationDetails)
- [`IntVariationDetails`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#IntVariationDetails)
- [`Float64VariationDetails`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#Float64VariationDetails)
- [`StringVariationDetails`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#StringVariationDetails)
- [`JSONArrayVariationDetails`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#JSONArrayVariationDetails)
- [`JSONVariationDetails`](https://pkg.go.dev/github.com/blackbirdworks/semaphore#JSONVariationDetails)

These methods take the same arguments and return `model.VariationResult[<type>]`:

| Field | Type | Description |
|---|---|---|
| `TrackEvents` | `bool` | `true` if this evaluation was tracked. |
| `VariationType` | `string` | Name of the variation used for this value. |
| `Failed` | `bool` | `true` if an error occurred during evaluation. |
| `Version` | `string` | Version of the flag used for evaluation. |
| `Reason` | `flag.ResolutionReason` | Resolution reason for this evaluation. |
| `ErrorCode` | `flag.ErrorCode` | Error code when an error occurred. |
| `ErrorDetails` | `string` | Additional detail about the error. |
| `Value` | `<type T>` | Resolved flag value in the expected type. |
| `Cacheable` | `bool` | `true` if the result can be cached (per user or globally depending on the reason). |

### Reason

`Reason` describes how the flag value was resolved:

| Reason | Description |
|---|---|
| `TARGETING_MATCH` | Resolved via a dynamic rule or user-targeting match (e.g., serve variation A when `username == "Thomas"`). |
| `TARGETING_MATCH_SPLIT` | Resolved via a dynamic rule serving a percentage split (e.g., serve variation A to 10% of users with `username == "Thomas"`). |
| `SPLIT` | Resolved via pseudorandom percentage assignment (e.g., serve variation A to 10% of all users). |
| `DISABLED` | The feature flag is disabled. |
| `DEFAULT` | Resolved to the flag's fallback/default because the flag is disabled in the management system. |
| `STATIC` | Evaluated to a static value with no dynamic evaluation (for example, the flag's default value). |
| `UNKNOWN` | An unknown issue occurred during evaluation. |
| `ERROR` | An error occurred during evaluation (`ErrorCode` contains details). |
| `OFFLINE` | Semaphore is evaluating in offline mode. |

## Get all flags for a specific user

To pass a snapshot of all flag values for a user to a front-end application, call `AllFlagsState`. It evaluates every flag for the user and returns a `flagstate.AllFlagsState` object:

```go
user := ffcontext.NewEvaluationContext("example")
allFlagsState := client.AllFlagsState(user)

// Serialize to JSON for a front-end application
forFE, err := allFlagsState.MarshalJSON()
```

`MarshalJSON()` produces output like:

```json
{
    "flags": {
        "test-flag0": {
            "value": true,
            "timestamp": 1622209328,
            "variationType": "True",
            "trackEvents": true
        },
        "test-flag1": {
            "value": "true",
            "timestamp": 1622209328,
            "variationType": "True",
            "trackEvents": true
        },
        "test-flag2": {
            "value": 1,
            "timestamp": 1622209328,
            "variationType": "True",
            "trackEvents": true
        },
        "test-flag3": {
            "value": [
                "yo",
                "ya"
            ],
            "timestamp": 1622209328,
            "variationType": "True",
            "trackEvents": true
        },
        "test-flag4": {
            "value": {
                "test": "yo"
            },
            "timestamp": 1622209328,
            "variationType": "True",
            "trackEvents": true
        },
        "test-flag5": {
            "value": 1.1,
            "timestamp": 1622209328,
            "variationType": "True",
            "trackEvents": false
        }
    },
    "valid": true
}
```

> [!CAUTION]
> Evaluating all flags at once does not emit tracking events.
