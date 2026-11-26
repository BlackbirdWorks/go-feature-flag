# Segments

## Overview

A segment is a named set of identifiers (user ids, emails, account ids) that targeting rules can test an attribute against. The members are not written in the flag file, so a large audience does not slow down loading flags, and the flags do not change when the audience does.

```yaml
beta-feature:
  variations:
    enabled: true
    disabled: false
  targeting:
    - name: beta users that did not churn
      query: targetingKey in segment("beta-users") and not (targetingKey in segment("churned"))
      variation: enabled
  defaultRule:
    variation: disabled
```

> [!NOTE]
> This page describes what the engine does today: the segment definition model, the `segment()` syntax, its validation and the evaluation rules. Looking members up in Valkey and the relay proxy settings that define segments are not available yet: they will come with the Valkey provider and the relay proxy wiring. Until then segments are available to the Go module (see [Use segments in the Go module](#use-segments-in-the-go-module)).

## Syntax

### Rules format

```text
<attribute> in segment("<name>")
```

- `<attribute>` is a context attribute, such as `targetingKey`, `email` or `user.id`. Its value is the identifier that is looked up.
- `<name>` is a string literal: 1 to 64 characters of `a-z`, `0-9`, `_` and `-`. It cannot be built from the context.
- The term works anywhere a comparison works: with `and`, `or`, `not` and parentheses.
- `in` can be written `IN`. Blanks inside the call are ignored.
- An attribute that is named `segment` is still an attribute: `segment eq "x"` and `segment in ["a", "b"]` are not segment terms. Only a word `segment` that is called, `segment(`, is the function.

```text
targetingKey in segment("beta-users")
targetingKey in segment("beta-users") and not (email in segment("churned"))
plan eq "pro" or user.id in segment("employees")
```

### JsonLogic format

```json
{"inSegment": [{"var": "targetingKey"}, "beta-users"]}
```

- The first operand is `{"var": "<attribute>"}`, the second is the name, a string literal.
- `inSegment` is allowed as the query, or as an operand of `and`, `or`, `!` and `!!`.
- It is not allowed anywhere else: not as an argument of another operation, and not in the iterators (`map`, `filter`, `reduce`, `all`, `some`, `none`), where it would run once for each element.

```json
{"and": [
  {"inSegment": [{"var": "targetingKey"}, "beta-users"]},
  {"!": {"inSegment": [{"var": "targetingKey"}, "churned"]}}
]}
```

### Limits

- A rule has at most 32 different segment terms. The same segment tested on the same attribute counts once.
- A segment term is not allowed in the `defaultRule`.
- The attribute value is used as it is when it is a string or an integer. An attribute that is missing, empty, or of another type is not a member of any segment, and no lookup is done for it.

## Load-time validation

The queries are parsed and compiled when the flags are loaded. A flag with an invalid segment term is rejected like any invalid flag, and `semaphore lint` reports it. The message names the rule and the place in the query:

```text
targeting rule "beta users" (index 0): invalid query: parsing the query: invalid segment() term at offset 29: invalid segment name "Bad": use 1 to 64 characters of a-z, 0-9, _ and -
targeting rule (index 2): invalid segment() term at $.and[1]: inSegment is only allowed as the query or as an operand of and, or, ! and !!, not inside the operation "map"
```

| Mistake | Message says |
|---|---|
| Name that is not a literal, such as `segment(beta)` | the segment name must be a string literal |
| Name with other characters, upper case, or more than 64 characters | invalid segment name |
| `key eq segment("a")`, `segment("a")` alone | segment() is only valid as the right side of "in" |
| Two arguments, or a missing parenthesis | segment() takes exactly one argument |
| `inSegment` in an iterator, or under another operation | not allowed inside the operation |
| A segment term in the `defaultRule` | segment terms are not allowed in the defaultRule |

In the Rules format the offset is the position in the query, counted in bytes from 0. In the JsonLogic format the place is a path such as `$.and[1]`.

A segment that is not defined (see below) is not an error: the flag loads, a warning is logged, and the segment matches nobody.

## Membership: member, not member, unknown

For each segment term the engine learns one of three answers:

| Answer | Meaning |
|---|---|
| member | the value is in the segment |
| not member | the value is not in the segment |
| unknown | the answer could not be obtained (the store is down, the lookup timed out, there is no provider) |

A rule combines its terms with three-valued logic. A result that does not depend on an unknown term is still known:

| Rule | A is unknown, the rest is | Result |
|---|---|---|
| `plan eq "free" and A` | `plan` is `"pro"` | no match (known) |
| `plan eq "pro" or A` | `plan` is `"pro"` | match (known) |
| `plan eq "pro" and A` | `plan` is `"pro"` | unknown |
| `not A` | | unknown |

When the result of a rule is unknown, **the rule is skipped**: it does not match and the next rule is evaluated. A rule can choose to match instead, which is what a deny rule needs (fail closed):

```yaml
- name: blocked users
  query: targetingKey in segment("blocked")
  onError: match
  variation: denied
```

`onError` is `skip` (the default) or `match`. It only matters for rules with segment terms.

The evaluation reason does not change: it stays `TARGETING_MATCH` or `DEFAULT`. A value that depended on a segment lookup is not cacheable.

### Failure policy of a segment

A segment definition can say what an unanswered lookup of that segment becomes, before the rule combines it:

| `onError` of the segment | An unanswered lookup is |
|---|---|
| `unknown` (default) | unknown |
| `notMember` | not a member |
| `member` | a member |

### Segments that are not defined

When a set of segment definitions is configured, a rule that uses another name is accepted with a warning, and the segment matches nobody (`not (x in segment("ghost"))` is then true for everybody). Without any definition, every segment is looked up through the provider as it is.

### Evaluation status

An evaluation that looked segments up reports how the lookups went, worst first: `STORE_ERROR` (a lookup was not answered), `NOT_CONFIGURED` (a rule uses a segment and no provider is configured), `HEALTHY`. The relay proxy will return it as the `semaphore_segmentStatus` metadata.

## Use segments in the Go module

```go
catalog, err := segment.NewCatalog(
    segment.Definition{Name: "beta-users", Type: segment.TypeFile},
    segment.Definition{Name: "churned", Type: segment.TypeFile, OnError: segment.OnErrorNotMember},
)
// ...
client, err := ffclient.New(&ffclient.Config{
    Retriever: &fileretriever.Retriever{Path: "flags.yaml"},
    Segments:  catalog,
    SegmentProvider: segment.NewMemoryProvider(map[string][]string{
        "beta-users": {"alice", "carol"},
        "churned":    {"carol"},
    }),
})
```

- `segment.Definition` has a `Name`, a `Type` (`file` or `valkey`) and an `OnError`. It never holds members.
- A `segment.Provider` answers `Contains(ctx, []segment.Lookup) error`. All the lookups of a rule are sent in one call, so a store can answer them in one round trip. A lookup the provider cannot answer stays unknown; a returned error makes all the lookups of the call unknown.
- `segment.NewMemoryProvider` holds the members in memory as hash sets. It serves the `file` type and tests; `segment.ReadMembers` reads a member file (one value per line).

## What is not available yet

- Loading `file` segments from an S3 object, and the `valkey` provider with its cache, circuit breaker and per-request deadline.
- The `segments` setting of the relay proxy, the segment lookups on the relay proxy evaluation endpoints, the `semaphore_segmentStatus` metadata, the `semaphore_cacheable: false` marker and the `segments` and `requiresServerEvaluation` fields of `/v1/flag/configuration`.
- Tools to import and swap members.
