# Segments

## Overview

A segment is a named set of identifiers (user ids, emails, account ids) that targeting rules can test an attribute against. The members are not written in the flag file, so a large audience does not slow down loading flags, and the flags do not change when the audience does.

There are two kinds of segment, chosen by the `type` of the segment definition:

| | [Static segment](segments-static.md) (`file`) | [Dynamic segment](segments-dynamic.md) (`redis`, `valkey`) |
|---|---|---|
| Where the members live | in the memory of the process, loaded from a member file | in the store (Redis or Valkey); never copied whole |
| Memory use | grows with the list | bounded cache of recent answers, when the resolver is used |
| Freshness | as of the last load of the file | as of the last lookup (cache TTL, when the resolver is used) |
| Cost of a lookup | a hash lookup in memory | one batched call to the store for the lookups of a rule (one round trip on a single node), plus the cache when the resolver is used |
| Use it for | small, rarely changing lists | large or fast changing audiences |

The rule syntax, the three answers and the failure policy on this page are the same for both kinds.

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
> This page describes what the engine does today: the segment definition model, the `segment()` syntax, its validation and the evaluation rules. The relay proxy settings that define segments, and looking members up in a store from the relay proxy, are not available yet: the relay wiring is tracked in issue 16. Go applications can use the dynamic providers today (see [Segments - dynamic](segments-dynamic.md)) and the static ones (see [Use segments in the Go module](#use-segments-in-the-go-module)).

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
- The attribute value is the identifier that is looked up. A string is used as it is. An integer of any kind is written in decimal. A float or a JSON number that holds a whole number is written in decimal too (`42`, never `4.2e+01` or `42.0`; a JSON document decodes its numbers as floats). A float is exact only up to 2^53: above that the identifier is the number that the float holds, which can differ from the number that was written, so use strings for large ids.
- An attribute inside an object that the Go client passes as a typed value (`map[string]string`, a struct with JSON tags, a pointer) is read from the JSON form of that object, so its names, its embedded fields, its omitted fields and its ties are exactly what `encoding/json` does, and its numbers keep the digits that JSON prints for them (an integer of any size is exact; `1.0` and `1e3` are `1` and `1000`, `1.5` is not an identifier). A `float32` holds what JSON prints for it (`float32(100000008)` is `100000010`): use integers, strings or `float64` for identifiers. A value that cannot be encoded inside that object (a cycle, a NaN, a channel, a `MarshalJSON` that panics, a nil pointer embedded in a struct whose type has a `MarshalJSON` method) makes the attribute unreadable: it has no identifier and is not looked up. Only that object is converted, once for the evaluation, so an unencodable value in another attribute of the context does not matter.
- An attribute that is missing, empty, or not one of these (a boolean, a number with a fraction, `NaN`, infinity, a list, an object) is not a member of any segment, and no lookup is done for it.

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

In the Rules format the offset is the position in the query as the engine parses it, counted in bytes (not characters) from 0: the engine first joins the lines of a query with a space (none where a line already ends with one or the join sits next to a bracket or a comma) and removes the leading spaces of each line, so a multi-line query counts from the joined text (a JsonLogic query is read as written), and a multibyte character before the term counts for all its bytes. The syntax errors of the parser itself count in characters. In the JsonLogic format the place is a path such as `$.and[1]`.

The rules that a [scheduled step](rollout-strategies/scheduled.md) carries are checked too when the flags are loaded: a segment term that is not valid, a segment term in the default rule of a step, and an `onError` that is not `skip` or `match` reject the flag, with `scheduled step <index>` in the message. So does a query that starts with `{` (after any spaces, tabs or line breaks) and does not decode as JsonLogic.

A segment that is not defined (see below) is not an error: the flag loads, a warning is logged, and the segment matches nobody.

## Membership: member or not a member

For each segment term the engine learns one of two answers, as LaunchDarkly's evaluator does (`evaluator_clause.go` and `evaluator_segment.go` of [go-server-sdk-evaluation](https://github.com/launchdarkly/go-server-sdk-evaluation)):

| Answer | Meaning |
|---|---|
| member | the value is in the segment |
| not a member | the value is not in the segment, **or the answer could not be obtained**: the store is down, the lookup timed out, the request ended, there is no provider, the segment is missing in the store (`missingKey: notMember`, the default) or the segment is not defined |

There is no third value by default. What cannot be answered is "not a member", and `not` flips it as usual: `not (targetingKey in segment("blocked"))` matches while the store is down.

**Outage note.** A rule that matches the people who are NOT in a segment (a `not`, or a `!=`-style exclusion such as `not (id in segment("blocked"))`) matches everybody while the store cannot answer, and a rule that matches the members matches nobody. Read the evaluation status (below) when you need to know that the answer was a fallback. A segment that must fail closed (a deny list) can say so with its failure policy or with the explicit opt-in below.

The evaluation reason does not change: it stays `TARGETING_MATCH` or `DEFAULT`. A value that depended on a segment lookup is not cacheable.

### Opt-in: unknown and `onError: match`

A segment definition can ask for the three-valued logic with `onError: unknown`. The lookups of that segment that cannot be answered are then unknown, a rule combines its terms with three-valued logic, and a result that does not depend on an unknown term is still known. A segment that is missing from the store is "not a member" by default, so `onError: unknown` does not apply to it (a known answer is never unknown): set `missingKey: unknown` in the store options to make a missing segment unknown.

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

`onError` of a rule is `skip` (the default) or `match`. It only matters for rules with a segment whose definition opted in with `onError: unknown`: with the default policy no term is unknown, so there is nothing for the rule to decide.

### What is looked up

A rule is evaluated left to right, like a rule without segments, with the short circuit of `and` and `or`, until it reaches a segment term.

- If it does not reach one, the rule is decided: the provider is not called, the evaluation has no segment status and the value stays cacheable. An operand that fails before that is an error of the rule, as in a rule without segments.
- If it reaches one, every term of the rule is looked up in one call to the provider (a term whose attribute is missing or is not an identifier is not a member and is not sent). The rule is then evaluated again, whole, with the answers.

So the order of the operands matters. Put the cheap conditions first: `plan eq "free" and k in segment("x")` looks nothing up when `plan` is not `free`, and `plan eq "pro" or k in segment("x")` looks nothing up when `plan` is `pro`; `k in segment("x") or plan eq "pro"` always looks up, because the segment comes first.

With the answers, an unknown term (a segment that opted in) does not stop the evaluation: `unknown or x` and `unknown and x` evaluate `x`, as the three-valued logic needs, and an operand that the answers make unnecessary is not evaluated (`k in segment("a") or user.id eq "staff"` does not read `user.id` for a member of `a`, as the list form of the rule does not). An operand that is reached and fails makes the rule fail, as in a rule without segments, whatever `onError` says: `onError` is for a result that is unknown, not for an error. A rule makes at most one call to the provider. A disabled rule is not evaluated, so it looks nothing up. The provider is not called either when the request context has ended (see below); the lookups are then not a member (unknown for a segment that opted in).

### Failure policy of a segment

A segment definition can say what an unanswered lookup of that segment becomes, before the rule combines it:

| `onError` of the segment | An unanswered lookup is |
|---|---|
| `notMember` (default) | not a member |
| `member` | a member |
| `unknown` | unknown (the opt-in above) |

### Segments that are not defined

When a set of segment definitions is configured, a rule that uses another name is accepted with a warning, and the segment matches nobody (`not (x in segment("ghost"))` is then true for everybody). Without any definition, every segment is looked up through the provider as it is.

### Evaluation status

The membership is two-valued, so the evaluation reports beside the result how the lookups went, with the names of LaunchDarkly's `bigSegmentsStatus`. The worst status of the segments of one evaluation wins:

| Status | Meaning | Rank |
|---|---|---|
| `HEALTHY` | every lookup was answered from current data | best |
| `STALE` | the answers were given, from data that is older than it should be (a snapshot that could not be refreshed and keeps its last known good set); the answers are used | |
| `NOT_CONFIGURED` | a rule uses a segment and no provider is configured | |
| `STORE_ERROR` | a lookup was not answered (store error, timeout, ended request, provider that panicked or left a lookup untouched) | worst |

An evaluation that looked no segment up has no status. The relay proxy returns it as the `semaphore_segmentStatus` metadata key of the evaluation responses of the four endpoints (OFREP single, OFREP bulk, `/v1/feature/{flagKey}/eval` and `/v1/allflags`) when the evaluation used a segment and the status is not `HEALTHY`; a healthy evaluation and a flag without a segment have no such key, so their responses are unchanged. OFREP does not send it when the Semaphore metadata is disabled. A provider that can serve older data reports it by implementing `segment.StaleReporter` (`Stale() bool`); nothing in the engine produces `STALE` by itself.

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

- `segment.Definition` has a `Name`, a `Type` (`file` for a static segment, `redis` or `valkey` for a dynamic segment) and an `OnError`. It never holds members.
- A `segment.Provider` answers `Contains(ctx, []segment.Lookup) error`. All the lookups of a rule are sent in one call, so a store can answer them in one round trip. A lookup the provider cannot answer is not a member (unknown for a segment that opted in); when it returns an error, the lookups that it answered before keep their answers and the others are not answered, whether the error comes from the store or from the end of the request context during the call (a provider that panics is not trusted: all lookups are unanswered, then the failure policy of the segment applies).
- A provider can fail in four ways, and all are the same to the engine: it returns an error, it panics (only the type of the panic value is logged), it leaves a lookup untouched, or it sets a state that is not one of the three constants. The lookups that were not answered with a member or a non member are then not a member by default (all of them after a panic), and the evaluation reports `STORE_ERROR`; the rule is evaluated with the answers it has, so an answer that decides the rule still decides it. A request context that has ended before the call is the same, without a call; one that ends during the call is an error of the provider like another, and the answers given before keep their value.
- `segment.NewMemoryProvider` holds the members in memory as hash sets. It serves the `file` type and tests. `segment.ReadMemberSet` reads a member file (one value per line; a UTF-8 byte order mark, blanks, carriage returns and blank lines are ignored, duplicates are one member) straight into a set, bounded by `segment.Limits` (rows and bytes, with defaults), and `segment.NewMemoryProviderFromSets` builds a provider from the sets.
- The segment package does not depend on the rules engine, so a provider package does not pull the parser in.

### For the relay proxy

- `flagdef.Context.RequestContext` is the context of the request: the lookups honour its end. The variation functions of the Go client do not carry a request context, so they use `context.Background()`; the relay proxy will set it for each request.
- `InternalFlag.SegmentTerms(svc)` returns the distinct (segment, attribute path) terms of a flag, scheduled rules included, so the relay can prefetch the lookups of a request. `SegmentNames` and `UsesSegments` are built on it. They parse the queries: call them when a flag is loaded.

## What is not available yet

- Loading `file` segments from an S3 object (the engine only reads a member file from a reader; fetching it needs the configuration layer and will come with the relay wiring and the membership tooling), and the relay proxy wiring of the `redis` provider (the provider, the cache and the circuit breaker are Go packages, see [Segments - dynamic](segments-dynamic.md)).
- The `segments` setting of the relay proxy, the segment lookups on the relay proxy evaluation endpoints, the `semaphore_cacheable: false` marker and the `segments` and `requiresServerEvaluation` fields of `/v1/flag/configuration`.
- Tools to import and swap members.
