# JsonLogic behaviour changes (`jsonlogic` v3.11.0)

The dependency update merged into this repository upgraded `github.com/diegoholiveira/jsonlogic/v3` from `v3.10.2` to `v3.11.0`.
This aligns number coercion, `substr`, `cat`, and `missing` / `missing_some` with the reference `json-logic-js` implementation.

A JsonLogic targeting rule (`query` written as JsonLogic) that relied on the old Go results can now evaluate differently. Rules that do not use these operators are unaffected. A rule that fails to evaluate is logged (`error while evaluating the jsonlogic query`) and counts as **not matching**, falling through to the next rule or the default rule.

The table shows the result of each rule with the given data across both versions:

| Rule | Data | v3.10.2 | v3.11.0 |
|---|---|---|---|
| `{"substr":["jsonlogic",10]}` | `{}` | `"jsonlogic"` (start past the end returned the whole string) | `""` |
| `{"substr":["jsonlogic",-10,2]}` | `{}` | `"jsonlogic"` | `"js"` (a negative start before the beginning counts as 0) |
| `{"substr":["jsonlogic",5,-5]}` | `{}` | runtime error (slice bounds out of range) | `""` |
| `{"cat":["Hello, ",{"var":"name"}]}` | `{"name":""}` | `"Hello,"` (result was trimmed) | `"Hello, "` |
| `{"cat":[" a","b "]}` | `{}` | `"ab"` | `" ab "` |
| `{"missing":["a","b","c"]}` | `{"a":1,"b":"","c":null}` | `["c"]` | `["b","c"]` (an empty string is missing, like null) |
| `{"missing_some":[2,["a","b","c"]]}` | `{"a":1,"b":"","c":null}` | `[]` | `["b","c"]` |
| `{"+":["Hey",1]}` | `{}` | `1` (an unparsable string counted as 0) | error `NaN`, the rule does not match |
| `{"+":"Hello"}` | `{}` | `0` | error `NaN` |
| `{"%":["Hey",1]}` | `{}` | `0` | error `NaN` |
| `{"==":[{"+":[{"var":"s"},1]},1]}` | `{"s":"abc"}` | `true` | `false` |
| `{"+":[1,"2",true,null,""]}` | `{}` | error (interface conversion) | `4` (true is 1, null and `""` are 0) |
| `{"*":["1",null]}` | `{}` | error (interface conversion) | `0` |
| `{"-":[true,false,true]}` | `{}` | error (interface conversion) | `0` |
| `{"/":["8",true,2]}` | `{}` | error (interface conversion) | `4` |

Unchanged in the cases we ran: `{"substr":["jsonlogic",-10]}` (`"jsonlogic"`), `{"substr":["jsonlogic",4,-2]}` (`"log"`),
`{"substr":["jsonlogic",9]}` (`""`), and comparisons such as `{">":[{"var":"age"},"17"]}` with `{"age":18}` (`true`).

## What to check

- Rules that build a string with `cat` and compare it (for example, a bucketing key ending in a space).
- Rules that use `missing` or `missing_some` on attributes that clients send as an empty string.
- Rules that add, subtract, multiply, or take a modulo of attributes that can hold free text: a non-numeric value now causes the rule to fail instead of silently counting as `0`.
- Rules that use `substr` with a start beyond the length or a negative length.

`semaphore-cli lint` does not evaluate rules, so use `semaphore-cli evaluate` with representative contexts before and after upgrading to compare results.
