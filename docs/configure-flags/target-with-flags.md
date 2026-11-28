# Target with Flags

## Overview

Flag targeting controls which users receive which variation based on the [evaluation context](../concepts/evaluation-context.md). Each flag can define a `targeting` list of rules evaluated against the context.

> [!NOTE]
> Targeting rules are evaluated in the order they are defined. The first matching rule determines the variation served.

## Define a targeting rule

Add a `targeting` list to your flag configuration. Each rule requires at least a `query` and one outcome (`variation`, `percentage`, or `progressiveRollout`):

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

## Rule format details

| Field | Required | Description |
|---|---|---|
| `query` | Yes | Rule condition in `rules` or `JsonLogic` format. See [Query formats](#query-formats). |
| `disabled` | No | Set to `true` to disable the rule. Default: `false`. |
| `name` | No | Optional rule name (required when updating a rule via [scheduled rollout](rollout-strategies/scheduled.md)). |
| `onError` | No | `skip` (default) or `match`: whether the rule matches when it depends on an unknown [segment](segments.md) lookup. |
| `variation` | No* | Name of the variation to serve. |
| `percentage` | No* | Map of variation names to percentage weights (e.g., `variationA: 10.59`, `variationB: 9.41`, `variationC: 80`). |
| `progressiveRollout` | No* | Ramp traffic percentage over time. See [Progressive rollout](rollout-strategies/progressive.md). |

> [!WARNING]
> **Outcome required**
> `variation`, `percentage`, and `progressiveRollout` are individually optional, but **at least one of the three must be set**.
>
> If multiple are present, Semaphore uses the first in this order: `progressiveRollout` > `percentage` > `variation`.

## Query formats

### Rules format

Semaphore's native query engine lives in `rules/` and is derived from [`nikunjy/rules`](https://github.com/nikunjy/rules) (see `NOTICE`).

- Operators are case-insensitive (for example, `eq` or `EQ`).
- Combine expressions with logical `AND` and `OR`.
- A query can span lines: the lines are joined with a space, so two lines never merge into one token. A line break inside a quoted value becomes exactly one space.

Comparison operators (`a`, `b` denote equivalent aliases):

| Operator | Description |
|:---:|---|
| `eq`, `==` | equals |
| `ne`, `!=` | not equals |
| `lt`, `<` | less than |
| `gt`, `>` | greater than |
| `le`, `<=` | less than or equal to |
| `ge`, `>=` | greater than or equal to |
| `co` | contains |
| `sw` | starts with |
| `ew` | ends with |
| `in` | in a list |
| `pr` | present |
| `not` | logical negation |
| `in segment("name")` | member of a [segment](segments.md) |

#### Examples

- Select a specific user: `key eq "example@example.com"`
- Select all identified users: `anonymous ne true`
- Select a user by custom property: `userId eq "12345"`
- Select by semver version: `version gt 0.4.0-beta.1`
- Combine multiple criteria (users with `key` ending in `@test.com`, `role` `"backend engineer"`, `env` `"pro"`, and `company` `"semaphore"`):

  ```bash
  (key ew "@test.com") and (role eq "backend engineer") and (env eq "pro") and (company eq "semaphore")
  ```

### JsonLogic format

Semaphore also supports [`JsonLogic`](http://jsonlogic.com/) queries via [`diegoholiveira/jsonlogic`](https://github.com/diegoholiveira/jsonlogic).

Write the `query` field as a valid JSON string representing a `JsonLogic` expression. The text is read as written, so it can span lines wherever JSON allows whitespace; a line break inside a string literal makes the query invalid. The rule matches when the expression evaluates to `true`; any other result is treated as `false`.

> [!NOTE]
> See the [JsonLogic operations reference](http://jsonlogic.com/operations.html) for all supported operators.

#### Examples

- Select a specific user: `{ "==" : [ { "var" : "key" }, "example@example.com"]}`
- Select all identified users: `{ "!=" : [ { "var" : "anonymous" }, true]}`
- Select a user by custom property: `{ "==" : [ { "var" : "userId" }, "12345"]}`
- Combine multiple criteria:

  ```json
  {"and": [{"endsWith": [{"var": "ids"}, "@test.com"]}, {"==": [{"var": "role"}, "backend engineer"]}, {"==": [{"var": "environment"}, "pro"]}, {"==": [{"var": "company"}, "semaphore"]}]}
  ```

- Select the members of a [segment](segments.md): `{"inSegment": [{"var": "targetingKey"}, "beta-users"]}`

## Environments

You can configure an environment when initializing Semaphore so every evaluation context automatically includes an `env` field.

### Relay Proxy

```yaml
version: 2
evaluation:
  environment: prod
```

### Go Module

```go
client, err := ffclient.New(&ffclient.Config{
  // ...
  Environment: "prod",
  // ...
})
```

You can then target specific environments in your rules:

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

## Rule name in evaluation metadata

If you add a `name` field to a targeting rule, Semaphore returns the matching rule's name in the `evaluatedRuleName` field of the evaluation metadata.
