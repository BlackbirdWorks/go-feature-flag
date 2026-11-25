package parser

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	debugError = "debug error"
	orOperator = " or "
	yIN12      = `y IN [1, 2, 3, 4, 5]`
)

func benchConditions() []string {
	return []string{
		xEq1,
		yIN12,
		`z in ["abcd", "efghijkl", "asdoihadoihasoihsaoasihasdoihdadoiahsdoihdasodha", "asdoisahdadoihasdoisdwoqeioheqcnac"]`,
		`(a > 123 or b <= 456)`,
		`(aa == 123123131.1232313131312312 or bb == 1231.1312313123123131231)`,
		`(jh gt "1.2.3" or kl le "1.4.56")`,
	}
}

// logicalRule joins conditions with "or", wrapping each one in parentheses
// depth times.
func logicalRule(conds []string, depth int) string {
	parts := make([]string, len(conds))
	for i, c := range conds {
		parts[i] = strings.Repeat("(", depth) + c + strings.Repeat(")", depth)
	}

	return strings.Join(parts, orOperator)
}

// bigRule repeats conditions 100 times, each wrapped in three parentheses.
// Every repetition must be joined with "or": since rules are validated
// strictly, a missing join makes NewEvaluator reject the rule.
func bigRule() string {
	conds := make([]string, 0, 100*len(benchConditions()))
	for range 100 {
		conds = append(conds, benchConditions()...)
	}

	return logicalRule(conds, 3)
}

func newBenchEvaluator(b *testing.B, rule string) *Evaluator {
	b.Helper()

	ev, err := NewEvaluator(rule)
	require.NoError(b, err)

	return ev
}

// checkBenchProcess fails the benchmark unless rule evaluates to want on
// input with no error and no debug error, so an input that does not match
// the rule cannot silently turn a benchmark into an error-path benchmark.
// It returns the evaluator, which has now had its first Process call.
func checkBenchProcess(b *testing.B, rule string, input map[string]any, want bool) *Evaluator {
	b.Helper()
	eval := newBenchEvaluator(b, rule)
	got, err := eval.Process(input)
	require.NoError(b, err)
	require.Equal(b, want, got)
	require.NoError(b, eval.LastDebugErr())

	return eval
}

func benchNewAndProcess(b *testing.B, rule string) {
	b.Helper()

	benchInput := newBenchInput()

	b.Helper()
	checkBenchProcess(b, rule, benchInput, false)
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		ev := newBenchEvaluator(b, rule)
		_, err := ev.Process(benchInput)
		require.NoError(b, err)
	}
}

func BenchmarkLogicalExpression(b *testing.B) {
	benchNewAndProcess(b, logicalRule(benchConditions(), 0))
}

func BenchmarkLogicalExpressionBig(b *testing.B) {
	benchNewAndProcess(b, bigRule())
}

func BenchmarkLogicalExpressionBigTreeCached(b *testing.B) {
	benchInput := newBenchInput()
	eval := checkBenchProcess(b, bigRule(), benchInput, false)
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_, err := eval.Process(benchInput)
		require.NoError(b, err)
		eval.Reset()
	}
}

func BenchmarkLogicalExpressionNested(b *testing.B) {
	benchNewAndProcess(b, logicalRule(benchConditions(), 3))
}

// BenchmarkProcess measures evaluation of an already-parsed rule, which is the
// hot path for callers that cache the *Evaluator. The DebugErr cases measure
// the cold path that builds a debug error, on purpose.
func BenchmarkProcess(b *testing.B) {
	benchInput := newBenchInput()

	rules := []struct {
		name      string
		rule      string
		want      bool
		wantDebug bool
	}{
		{name: "IntEq", rule: xEq1},
		{name: "IntIn", rule: yIN12},
		{name: "StringEq", rule: `z eq "asdada"`, want: true},
		{
			name: "StringIn",
			rule: `z in ["abcd", "efghijkl", "asdada", "asdoisahdadoihasdoisdwoqeioheqcnac"]`,
			want: true,
		},
		{name: "FloatCmp", rule: `aa == 123123131.1232313131312312`},
		{name: "Version", rule: `jh gt 1.2.0`, want: true},
		{name: "Nested", rule: uPlanEqPro, want: true},
		{name: "Logical", rule: `(x eq 1 or y eq 2) and (z eq "nope" or b > 100)`},
		{name: "Mixed", rule: strings.Join(benchConditions(), orOperator)},
		{name: "DebugErr/TypeMismatch", rule: `z gt 123`, wantDebug: true},
		{name: "DebugErr/Missing", rule: `missing eq "x"`, wantDebug: true},
	}
	for _, tt := range rules {
		b.Run(tt.name, func(b *testing.B) {
			eval := newBenchEvaluator(b, tt.rule)
			got, err := eval.Process(benchInput)
			require.NoError(b, err)
			require.Equal(b, tt.want, got)
			require.Equal(b, tt.wantDebug, eval.LastDebugErr() != nil, debugError)
			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				_, procErr := eval.Process(benchInput)
				require.NoError(b, procErr)
			}
		})
	}
}

// BenchmarkProcessParallel measures Process on one evaluator shared by
// concurrent callers; run it with -cpu 1,8 to see contention.
func BenchmarkProcessParallel(b *testing.B) {
	const rule = `x eq 1 and y in [1, 2, 3]`

	items := map[string]any{"x": 1, "y": 2}
	ev := checkBenchProcess(b, rule, items, true)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := ev.Process(items)
			assert.NoError(b, err)
		}
	})
}

// newBenchInput returns a correctly typed value for every attribute the
// benchmark rules read, so they evaluate without errors or debug errors.
// Each condition of benchConditions is false for it, so an or-chain of them is
// walked to the end:
//
//	x eq 1                       x is 123
//	y IN [1, 2, 3, 4, 5]          y is 2345
//	z in ["abcd", ...]            z is "asdada"
//	a > 123 or b <= 456           a is 100, b is 123131
//	aa == ... or bb == ...        aa is 123123.123131232113, bb is 1.5
//	jh gt "1.2.3" or kl le "1.4.56"
//	                              (string comparisons) jh is "1.2.3",
//	                              kl is "1.5" > "1.4.56"
func newBenchInput() map[string]any {
	return map[string]any{
		"x":  123,
		"y":  2345,
		"z":  "asdada",
		"a":  100,
		"b":  123131,
		"aa": 123123.123131232113,
		"bb": 1.5,
		"jh": versionOneTwoThree,
		"kl": "1.5",
		"u":  map[string]any{plan: pro, age: 31},
	}
}

// BenchmarkNewEvaluator measures construction only (parse and validate).
func BenchmarkNewEvaluator(b *testing.B) {
	rule := strings.Join(benchConditions(), orOperator)

	b.ReportAllocs()

	for range b.N {
		newBenchEvaluator(b, rule)
	}
}

// BenchmarkEvaluate measures the one-shot Evaluate helper.
func BenchmarkEvaluate(b *testing.B) {
	benchInput := newBenchInput()
	rule := strings.Join(benchConditions(), orOperator)
	checkBenchProcess(b, rule, benchInput, false)
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		Evaluate(rule, benchInput)
	}
}

// BenchmarkNewEvaluatorFirstProcess measures a new evaluator plus its first
// Process call, i.e. what any one-use evaluator pays.
func BenchmarkNewEvaluatorFirstProcess(b *testing.B) {
	benchInput := newBenchInput()
	rule := strings.Join(benchConditions(), orOperator)
	checkBenchProcess(b, rule, benchInput, false)
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		ev := newBenchEvaluator(b, rule)
		_, err := ev.Process(benchInput)
		require.NoError(b, err)
	}
}

// TestBenchmarkVersionRule checks that the bare semver constant used by
// BenchmarkProcess/Version goes through the semver comparison.
func TestBenchmarkVersionRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		jh        string
		want      bool
		wantDebug bool
	}{
		{name: "semver ordering", jh: "1.10.0", want: true},
		{name: "not a semver", jh: abc, wantDebug: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ev, err := NewEvaluator(`jh gt 1.2.0`)
			require.NoError(t, err)
			got, err := ev.Process(map[string]any{"jh": tt.jh})
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantDebug, ev.LastDebugErr() != nil, debugError)
		})
	}
}

// TestBenchmarkRules checks that the rules built for the logical expression
// benchmarks are accepted by the strict parser and evaluate the benchmark input
// to false (every condition is walked) without an error or debug error.
func TestBenchmarkRules(t *testing.T) {
	benchInput := newBenchInput()

	t.Parallel()

	tests := []struct {
		name string
		rule string
	}{
		{name: "logical", rule: logicalRule(benchConditions(), 0)},
		{name: nested2, rule: logicalRule(benchConditions(), 3)},
		{name: "big", rule: bigRule()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ev, err := NewEvaluator(tt.rule)
			require.NoError(t, err)
			got, err := ev.Process(benchInput)
			require.NoError(t, err)
			assert.False(t, got)
			assert.NoError(t, ev.LastDebugErr())
		})
	}
}
