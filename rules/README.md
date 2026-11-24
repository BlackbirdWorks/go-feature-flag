> Vendored copy of github.com/go-feature-flag/rules at commit 3baa4ca8fb5ee47aca90ff6d3227fd4b0be5523b (pull request #2, "perf: hash index for large IN lists"), which is the previously pinned commit e9704fa7a06c plus that PR.
>
> Local changes: integer literals (single values and `in` lists) are parsed with `strconv.Atoi` instead of `strconv.ParseInt(..., 64)` followed by an `int(...)` conversion, so an out-of-range literal is rejected with an error on every platform instead of being narrowed (CodeQL `go/incorrect-integer-conversion`). `LICENSE` is unchanged.
>
> Origin and licence: derived from `github.com/nikunjy/rules` (MIT, Copyright (c) 2019 Nikunj Yadav; see `LICENSE` in this directory and the root `NOTICE`), through the maintained fork `github.com/go-feature-flag/rules`. The directory is part of the root Go module of this repository (import path `github.com/blackbirdworks/semaphore/rules`); it has no `go.mod` of its own, and it is linted and tested with the rest of the repository. Only the antlr-generated parser files (`parser/jsonquery_lexer.go`, `parser/jsonquery_parser.go`, `parser/jsonquery_visitor.go`) are excluded from lint and coverage.
>
> Further local changes: the import path above; the string hash seed of long `in` lists belongs to each compiled set instead of a package variable; lint fixes (no change of results); `ErrInvalidOperand` is named `InvalidOperandError`.


# Golang Rules Engine

Rules engine written in golang with the help of antlr.

This package will be very helpful in situations where you have a generic rule and want to verify if your values (specified using `map[string]interface{}`) satisfy the rule.

Here are some examples:

```
  parser.Evaluate("x eq 1", map[string]interface{}{"x": 1})
  parser.Evaluate("x == 1", map[string]interface{}{"x": 1})
  parser.Evaluate("x lt 1", map[string]interface{}{"x": 1})
  parser.Evaluate("x < 1", map[string]interface{}{"x": 1})
  parser.Evaluate("x gt 1", map[string]interface{}{"x": 1})

  parser.Evaluate("x.a == 1 and x.b.c <= 2", map[string]interface{}{
    "x": map[string]interface{}{
       "a": 1,
       "b": map[string]interface{}{
          "c": 2,
       },
    },
  })


  parser.Evaluate("y == 4 and (x > 1)", map[string]interface{}{"x": 1})

  parser.Evaluate("y == 4 and (x IN [1,2,3])", map[string]interface{}{"x": 1})

  parser.Evaluate("y == 4 and (x eq 1.2.3)", map[string]interface{}{"x": "1.2.3"})

```

## Operations

All the operations can be written capitalized or lowercase (ex: `eq` or `EQ` can be used)

Logical Operations supported are `and` `or`

Compare Expression and their definitions

| expression | meaning   | 
-------------|------------
| eq         | equals to |
| ==         | equals to |
| ne         | not equal to |
| !=         | not equal to |
| lt         | less than |
| <          | less than |
| gt         | greater than |
| >          | greater than |
| le         | less than or equal to |
| <=          | less than or equal to  |
| ge          | greater than or equal to|
| >=          | greater than or equal to|
| co         | contains  |
| sw         | starts with  |
| ew         | ends with  |
| in         | in a list  |
| pr         | present, will be true if you have a key as true  |
| not         | not of a logical expression  |

## How to use it

Use your dependency manager to import `github.com/blackbirdworks/semaphore/rules/parser`. This will let you parse a rule and keep the parsed representation around.
Alternatively, you can also use `github.com/blackbirdworks/semaphore/rules` directly to call the root `Evaluate(string, map[string]interface{})` method.

I would recommend importing `github.com/blackbirdworks/semaphore/rules/parser`

## How to extend the grammar

1. Please look at this [antlr tutorial](https://tomassetti.me/antlr-mega-tutorial/#setup-antlr), the link will show you how to setup antlr.
   The article has a whole lot of detail about antlr I encourage you to read it, you might also like [my blog post](https://medium.com/@nikunjyadav/generic-rules-engine-in-golang-using-antlr-d30a0d0bb565) about this repo.
2. After taking a look at the antlr tutorial, you can extend the [JsonQuery.g4 file](https://github.com/nikunjy/rules/blob/master/parser/JsonQuery.g4).
3. Compile the parser `antlr4 -Dlanguage=Go -visitor -no-listener JsonQuery.g4 -o ./` (Note: `-o` is the output directory, make sure all the stuff it generates is in the `parser` directory of the root repo folder)

