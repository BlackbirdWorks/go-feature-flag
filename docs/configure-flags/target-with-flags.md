# Target with Flags

## Overview
This category explains how to use flag targeting to control which of your customers receive which variation of a feature flag.

Targeting is done based on the [evaluation context](../concepts/evaluation-context.md) associated to the evaluation request.
Semaphore evaluation context are data objects representing users, devices, organizations, and other entities that interact with your app.

Each feature flag can have a set of targeting rules that define the conditions under which a variation should be served.
When a flag is evaluated, the targeting rules are evaluated against the evaluation context to determine which variation to serve.

> [!NOTE]
> The targeting rules are evaluated in the order they are defined in the flag configuration.  
> The first rule that matches the evaluation context will determine the variation to serve.

## Define a targeting rule

A targeting rule is a configuration that allows you to serve a variation based on some conditions.  
You can have as many rules as you want in your flag configuration.

To create your first rule you need to add a `targeting` field in your flag configuration.

```yaml
scream-level-feature:
  variations:
    low: "whisper"
    medium: "talk"
    high: "scream"
  targeting:
    - query: targetingKey eq "12345"
      variation: high
    - query: targetingKey eq "678910"
      variation: medium
  defaultRule:
    variation: low
```

A minimum rule configuration requires a `query` field and a `variation` field to define the condition and the variation to serve.

If you have more advanced needs you can also have a percentage repartition or have a progressive rollout for this, check the [rule format details](#rule-format-details) section for more information.

## Rule Format Details

| Field | Required | Description |
|---|---|---|
| `query` | Yes | Rule condition in either `rules` format or `JsonLogic` format. See [Query formats](#query-formats). |
| `disabled` | No | Set to `true` to disable the rule. Default: `false`. |
| `name` | No | Optional rule name (required when updating a rule with a [scheduled rollout](rollout-strategies/scheduled.md)). |
| `variation` | No* | Name of the variation to serve. |
| `percentage` | No* | Map of variation names to percentage weights (e.g. `variationA: 10.59`, `variationB: 9.41`, `variationC: 80`). |
| `progressiveRollout` | No* | Ramp traffic percentage over time. See [Progressive rollout](rollout-strategies/progressive.md). |

> [!WARNING]
> **Don't forget to return a variation.**
> `variation`, `percentage` and `progressiveRollout` are optional but you **must have at least one of the three**.
>
> If you have more than one field we will use the first one in the order
> `progressiveRollout` > `percentage` > `variation`.

## Query formats

### Rules format

Semaphore supports a rule query format whose engine lives in the `rules/` directory of the repository. It is derived from the Go library [`nikunjy/rules`](https://github.com/nikunjy/rules) (see `NOTICE`).

All the operations can be written in capitalized or lowercase characters (ex: `eq` or `EQ` can be used).
Logical Operations supported are `AND` & `OR`.

Compare Expression and their definitions (`a|b` means you can use one of either `a` or `b`):

|  Operator  | Description                 |
|:----------:|-----------------------------|
| `eq`, `==` | equals to                   |
| `ne`, `!=` | not equals to               |
| `lt`, `<`  | less than                   |
| `gt`, `>`  | greater than                |
| `le`, `<=` | less than equal to          |
| `ge`, `>=` | greater than equal to       |
|    `co`    | contains                    |
|    `sw`    | starts with                 |
|    `ew`    | ends with                   |
|    `in`    | in a list                   |
|    `pr`    | present                     |
|   `not`    | not of a logical expression |

#### Examples

- Select a specific user: `key eq "example@example.com"`
- Select all identified users: `anonymous ne true`
- Select a user with a custom property: `userId eq "12345"`
- Select by version: `version gt 0.4.0-beta.1`
- Select on multiple criteria:
  *All users with ids finishing by `@test.com` that have the role `backend engineer` in the `pro` environment for the
  company `semaphore`*

  ```bash
  (key ew "@test.com") and (role eq "backend engineer") and (env eq "pro") and (company eq "semaphore")
  ```

### JsonLogic format

Semaphore also supports the [`JsonLogic`](http://jsonlogic.com/) format, based on the GO library [`diegoholiveira/jsonlogic`](https://github.com/diegoholiveira/jsonlogic) library.

To use a JsonLogic query, you need to write your query in a JSON format as a JSON string in the `query` field.  
The query must be a valid JSON string that represents a `JsonLogic` expression.

The rule should return `true` if the user matches the rule, anything else will be considered as `false`.

> [!NOTE]
> **JsonLogic Operations**
> Check all the supported operations in the [JsonLogic documentation](http://jsonlogic.com/operations.html).

#### Examples
- Select a specific user: `{ "==" : [ { "var" : "key" }, "example@example.com"]}`
- Select all identified users: `{ "!=" : [ { "var" : "anonymous" }, true]}`
- Select a user with a custom property: `{ "==" : [ { "var" : "userId" }, "12345"]}`
- Select on multiple criteria:
  *All users with ids finishing by `@test.com` that have the role `backend engineer` in the `pro` environment for the
  company `semaphore`*
  
  ```json
  {"and": [{"endsWith": [{"var": "ids"}, "@test.com"]}, {"==": [{"var": "role"}, "backend engineer"]}, {"==": [{"var": "environment"}, "pro"]}, {"==": [{"var": "company"}, "semaphore"]}]}
  ```

## Environments

When you initialise `semaphore` you can set an **environment** for this Semaphore instance.

### Relay Proxy

    ```yaml
    # ...
    evaluationContextEnrichment:
      env: prod
    # ...
    ```

### Go Module

    ```go
    client, err := ffclient.New(&ffclient.Config{
      // ...
      // highlight-next-line
      Environment:    "prod",
      // ...
    })
    ```

When an environment is set, it adds a new field in your evaluation context called **`env`** that you can use in your queries.
It means that you can decide to activate a flag only for some **environment**.

**Example of flag configuration based on the environment:**

```yaml
my-flag:
  variations:
    A: "A"
    B: "B"
    C: "C"
  targeting:
    - name: Target pre environment
      query: env eq "pre"
      variation: A
    - name: Target pro environment
      query: env eq "pro"
      variation: B
  defaultRule:
    variation: C
```

## Get the rule name in the metadata

When you use a rule in your targeting, you can get the name of the rule in the metadata of the variation.  
The information on what rule has been used to serve the variation is available in the metadata of the variation in the field called `evaluatedRuleName`.

If you are interested about this information, you have to name your rules by adding the field `name` in your rule. This name will be extract and added in the `evaluatedRuleName` field of the metadata.
