package parser

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

const (
	fixtureInt = "int"
	str2       = "str"
)

func inBenchSizes() []int { return []int{8, 16, 32, 64, 1000, 10000, 300000} }

func inBenchList(kind string, n int) string {
	parts := make([]string, n)
	for i := range parts {
		if kind == str2 {
			parts[i] = `"user-` + strconv.Itoa(i) + `"`
		} else {
			parts[i] = strconv.Itoa(i)
		}
	}

	return "[" + strings.Join(parts, ", ") + "]"
}

// BenchmarkIn measures one Process call on a compiled evaluator for IN and
// NOT IN, with the looked-up value present (hit) and absent (miss). The hit
// is the last element, the worst case for a linear scan.
func BenchmarkIn(b *testing.B) {
	for _, kind := range []string{str2, fixtureInt} {
		for _, size := range inBenchSizes() {
			list := inBenchList(kind, size)

			var hit, miss any
			if kind == str2 {
				hit, miss = "user-"+strconv.Itoa(size-1), "nobody"
			} else {
				hit, miss = size-1, -1
			}

			for _, operator := range []string{"in", "not in"} {
				rule := "x " + operator + " " + list
				if operator == "not in" {
					rule = "not (x in " + list + ")"
				}

				eval := newBenchEvaluator(b, rule)

				for _, probe := range []struct {
					val  any
					name string
				}{{name: "hit", val: hit}, {name: "miss", val: miss}} {
					input := map[string]any{"x": probe.val}
					eval.Process(input) // compile

					name := fmt.Sprintf(
						"%s/%s/%s/n=%d",
						kind,
						strings.ReplaceAll(operator, " ", ""),
						probe.name,
						size,
					)
					b.Run(name, func(b *testing.B) {
						b.ReportAllocs()

						for range b.N {
							eval.Process(input)
						}
					})
				}
			}
		}
	}
}

// BenchmarkInCompile measures NewEvaluator plus the first Process call, which
// is when the rule is compiled (and the hash set built, if any).
func BenchmarkInCompile(b *testing.B) {
	for _, kind := range []string{str2, fixtureInt} {
		for _, size := range inBenchSizes() {
			rule := xIn + inBenchList(kind, size)

			input := map[string]any{"x": -1}
			if kind == str2 {
				input["x"] = "nobody"
			}

			b.Run(fmt.Sprintf("%s/n=%d", kind, size), func(b *testing.B) {
				b.ReportAllocs()

				for range b.N {
					newBenchEvaluator(b, rule).Process(input)
				}
			})
		}
	}
}
