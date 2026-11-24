package parser

import (
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/antlr4-go/antlr/v4"
	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testStringer struct{ s string }

func (s testStringer) String() string { return s.s }

// visitorProcess runs a rule through the original ANTLR visitor, exactly as
// Evaluator.Process did before rules were compiled.
//
//nolint:nonamedreturns // recover sets the result
func visitorProcess(tree antlr.ParseTree, items map[string]any) (ret bool, retErr error, debugErr error) {
	defer func() {
		if info := recover(); info != nil {
			retErr = recoveredPanicError{info: info}
			ret = false
		}
	}()
	visitor := NewJSONQueryVisitorImpl(items)
	result := visitor.Visit(tree)
	debugErr = visitor.debugErr
	if result == nil || visitor.err != nil {
		return false, visitor.err, debugErr
	}

	return result.(bool), visitor.err, debugErr
}

// errString renders err, tolerating the existing InvalidOperandError.Error
// panic on a nil operand so both implementations can still be compared.
func errString(err error) (s string) { //nolint:nonamedreturns // recover sets the result
	defer func() {
		if info := recover(); info != nil {
			s = fmt.Sprintf("Error() panicked: %v", info)
		}
	}()
	if err == nil {
		return "<nil>"
	}

	return err.Error()
}

// TestEvaluatorProcess covers Process. Each case is a self-contained,
// table-driven check; they are grouped here so Process has one test.
func TestEvaluatorProcess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{name: "differential", run: testProcessDifferential},
		{name: "zero value evaluator", run: testProcessZeroValue},
		{name: "debug error does not leak rule lists", run: testProcessDoesNotLeakRuleConstants},
		{name: "operand error does not leak rule lists", run: testProcessDoesNotLeakRuleConstantsViaOperandErrors},
		{name: "fresh error per call", run: testProcessErrorIdentity},
		{name: "concurrent", run: testProcessConcurrent},
		{name: "concurrent first call", run: testProcessConcurrentFirstCall},
		{name: "compile failure falls back to the visitor", run: testProcessCompileFailureFallsBack},
		{name: "tree dropped after compile", run: testProcessDropsTreeAfterCompile},
		{name: "version fast path", run: testProcessVersionFastPath},
		{name: "no stale debug error", run: testProcessStaleDebugErr},
		{name: "compiled lists have no spare capacity", run: testProcessListCapacity},
		{name: "concurrent append to a stale list", run: testProcessConcurrentStaleList},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.run(t)
		})
	}
}

// testProcessDifferential compares the compiled evaluator, fresh and reused,
// and Evaluate against the visitor over a broad rule and input corpus.
func testProcessDifferential(t *testing.T) {
	t.Helper()
	type kv = map[string]any
	inputs := []kv{
		{},
		nil,
		{
			"i": 5, "i64": int64(7), "i32": int32(3), "f": 2.5, "s": "Hello", "b": true, "n": nil,
			"v": "1.2.3", "l": []int{1, 2}, "m": kv{"k": "v"}, "bad": "x.y",
			"u":   kv{"plan": "pro", "age": 31, "n": nil, "deep": kv{"x": 1, "s": "abc", "o": kv{}}},
			"str": "5", "stringer": fmt.Stringer(nil), "e": "",
			"nilmap": kv(nil), "nm": map[string]string{"a": "b"},
			"st": testStringer{"HELLO"}, "f32": float32(1.5), "esc": "a\"b", "uni": "A",
			"r": deepMap(12), "q": deepMap(20),
		},
		{"i": "text", "f": "text", "s": 12, "b": "true", "v": 5, "u": "notamap", "u2": 3.5},
	}

	ops := []string{"eq", "==", "ne", "!=", "gt", ">", "lt", "<", "ge", ">=", "le", "<=", "co", "sw", "ew", "in", "IN"}
	attrs := []string{
		"i",
		"i64",
		"i32",
		"f",
		"s",
		"b",
		"n",
		"v",
		"l",
		"m",
		"missing",
		"str",
		"e",
		"u.plan",
		"u.age",
		"u.n",
		"u.deep.x",
		"u.deep.s",
		"u.deep.o.z",
		"u.missing.y",
		"missing.a.b",
		"u2.a",
		"i.x",
		"nilmap.a",
		"nm.a",
		"st",
		"f32",
		"esc",
		"uni",
		"r.r.r.r.r.z",
		"r.r.r.r.r.r.n.x",
	}
	values := []string{
		`1`,
		`-3`,
		`5`,
		`0`,
		`1e3`,
		`-2E+1`,
		`2E+1`,
		`"a\"b"`,
		`"\u0041"`,
		`"\\"`,
		`2.5`,
		`-0.5`,
		`1.0e2`,
		`"hello"`,
		`"HELLO"`,
		`""`,
		`"1.2.4"`,
		`1.2.3`,
		`0.0.1`,
		`true`,
		`false`,
		`null`,
		`[1, 2, 3]`,
		`[5]`,
		`[1.5, 2.5]`,
		`[2.5]`,
		`["a", "hello"]`,
		`["x"]`,
		`99999999999999999999`,
		`[1, 99999999999999999999]`,
		`[99999999999999999999, 2]`,
		`1e999`,
		`1.5e999`,
		`[1.5, 1.5e999]`,
		`[1.5e999]`,
	}

	type group struct {
		name   string
		rules  []string
		inputs []kv
	}
	var groups []group
	for _, a := range attrs {
		rules := make([]string, 0, 2+len(values)*len(ops))
		rules = append(rules, a+" pr", "not ("+a+" pr)")
		for _, op := range ops {
			for _, v := range values {
				rules = append(rules, fmt.Sprintf("%s %s %s", a, op, v))
			}
		}
		groups = append(groups, group{name: "attr/" + a, rules: rules, inputs: inputs})
	}
	// combinations: stale state after errors and aborted paths, short circuits.
	combinations := []string{
		`i eq 1 or s eq "hello"`,
		`i in [1, 2] or i in [5]`,
		`s in ["a"] or s in ["Hello"]`,
		`f in [1.5] or f in [2.5]`,
		`i in [1] or s in ["a"]`,
		`i in [1] or f in [2.5] or s in ["a", "b"]`,
		`u.deep.x eq 2 or i eq 5`,
		// failed paths whose last step is missing leave entries on the
		// attribute stack; repeated, they push it past its inline size
		`r.r.n.x eq 1 or r.r.n.x eq 1 or r.r.n.x eq 1 or r.r.n.x eq 1 or r.z eq 1`,
		`r.r.n.x eq 1 or r.r.n.x eq 1 or r.r.n.x eq 1 or r.r.n.x eq 1 or r.r.z pr or r.z eq 1 or i eq 5`,
		`r.r.r.n.x pr or r.r.r.n.x pr or r.r.r.n.x pr or r.z eq 0 or r.r.z eq 0`,
		`r.r.r.r.r.r.r.n.x eq 1 or r.r.r.r.r.r.z eq 1 or r.z pr`,
		`r.r.r.r.r.r.r.z eq 0 and r.r.r.r.r.r.r.r.r.z eq 0`,
		// a spilled stack that was cleared must not hand back old spilled
		// entries when it spills again (r and q differ at every level)
		`r.r.r.r.r.r.z eq 0 or q.r.r.r.r.r.z eq 15`,
		`r.r.r.r.r.r.z eq 0 or r.r.r.r.r.r.r.z eq 0 or r.r.r.r.r.r.r.r.z eq 1 or q.r.r.r.r.r.r.r.z eq 13`,
		`u.deep.x eq 2 or u.plan eq "pro"`,
		`u.deep.x eq 1 and u.deep.s eq "ABC"`,
		`u.deep.x eq 2 or u.deep.o.z pr or i pr`,
		`u.deep.o.z eq 1 or u.deep.x eq 2 or i pr`,
		`u.missing.y.z eq 1 or i pr`,
		`u.missing.y.z eq 1 or u.plan eq "pro"`,
		`missing.a.b.c eq 1 or i eq 5 or s eq "hello"`,
		`i.a.b eq 1 or i eq 5`,
		`i eq 1 and s eq "hello"`,
		`i eq 5 and s eq "hello"`,
		`not (i eq 5)`,
		`NOT (i eq 5 or f gt 1)`,
		`(i eq 5)`,
		`((i eq 5) and (s eq "hello" or b eq true))`,
		`u.plan eq "pro" and u.age gt 18`,
		`u.missing.y eq 1 or x pr`,
		`u.missing.y eq 1 or i eq 5`,
		`u.missing.y eq 1 or u.plan eq "pro"`,
		`missing.a.b pr or i pr`,
		`missing.a.b pr or missing pr`,
		`u.n.x eq 1 or i pr`,
		`i.x pr or s eq "hello"`,
		`i eq 99999999999999999999 or i pr`,
		`i eq 99999999999999999999 or s eq "hello"`,
		`i in [1, 99999999999999999999] or f in [1.5, 2.5]`,
		`i in [1, 99999999999999999999] or s in ["a"]`,
		`i in [1, 99999999999999999999] or i in [5]`,
		`i in [1, 99999999999999999999] or i in [5, 6]`,
		`i in [99999999999999999999] or i in [5, 6]`,
		`i in [99999999999999999999] or i eq 5`,
		`f in [1.5, 1.5e999] or s in ["a"]`,
		`f in [1.5, 1.5e999] or f in [1.5]`,
		`i gt "a" or s eq 5`,
		`b gt true or s eq "hello"`,
		`n eq null and missing eq null`,
		`n ne null or s co "ell"`,
		`i eq 1 or i eq 2 or i eq 3 or i eq 5`,
		`(i eq 1 or i eq 2) and (s eq "nope" or f > 1)`,
		`y IN [1, 2, 3, 4, 5]`,
		`z in ["abcd", "efghijkl", "asdada"]`,
		`(aa == 123123131.1232313131312312 or bb == 1231.1312313123123131231)`,
		`(jh gt "1.2.3" or kl le "1.4.56")`,
		`v gt 1.2.0`,
		`v lt 1.2.0 or v eq 1.2.3`,
		`bad eq 1.2.3`,
		`  i   eq   5  `,
		"i eq 5\nor s eq \"x\"",
		`x eq`,
		``,
	}

	// versions: the compiled path pre-parses valid rule versions and parses
	// plain left versions without semver.Make; invalid rule versions keep
	// VersionOperation.
	versionRules := make([]string, 0, 14*len(versionRuleValues())+3)
	for _, op := range []string{"eq", "==", "ne", "!=", "gt", ">", "lt", "<", "ge", ">=", "le", "<=", "co", "in"} {
		for _, v := range versionRuleValues() {
			versionRules = append(versionRules, fmt.Sprintf("v %s %s", op, v))
		}
	}
	versionRules = append(versionRules,
		`v gt 1.2.0 and w lt 2.0.0`,
		`v gt 99999999999999999999.0.0 or w lt 2.0.0`,
		`v eq 1.2.3 or missing gt 1.0.0`,
	)
	versionInputs := make([]kv, 0, 2+len(versionLeftValues()))
	versionInputs = append(versionInputs, nil, kv{})
	for _, left := range versionLeftValues() {
		versionInputs = append(versionInputs, kv{"v": left, "w": left})
	}
	groups = append(groups, group{name: "versions", rules: versionRules, inputs: versionInputs})
	groups = append(groups, group{name: "combinations", rules: combinations, inputs: inputs})
	rnd := rand.New(rand.NewSource(1))
	count := randomRuleCount
	if testing.Short() {
		count = randomRuleCount / 10
	}
	randRules := make([]string, 0, count)
	for len(randRules) < count {
		randRules = append(randRules, genRule(rnd, 4))
	}
	randInputs := make([]kv, 0, 8)
	randInputs = append(randInputs, nil)
	for range 7 {
		randInputs = append(randInputs, genMap(rnd, 3))
	}
	// chunks run in parallel; one sequential group dominated the run time
	const chunk = 250
	for start := 0; start < len(randRules); start += chunk {
		end := min(start+chunk, len(randRules))
		groups = append(
			groups,
			group{name: fmt.Sprintf("random/%d", start), rules: randRules[start:end], inputs: randInputs},
		)
	}

	for _, tt := range groups {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, rule := range tt.rules {
				// repeated is reused for every input; fresh compiles on its
				// first Process, which is the call under test for each input.
				repeated, err := NewEvaluator(rule)
				if err != nil {
					// invalid rules never reach Process
					continue
				}
				// compiling and visiting only read the tree, so the visitor
				// reference and every fresh evaluator share one parse, as
				// NewEvaluator would have built it
				tree := repeated.tree
				for i, input := range tt.inputs {
					wantRet, wantErr, wantDebug := visitorProcess(tree, input)
					fresh := &Evaluator{rule: rule, tree: tree}
					for name, ev := range map[string]*Evaluator{"fresh": fresh, "repeated": repeated} {
						gotRet, gotErr := ev.Process(input)
						assert.Equal(t, wantRet, gotRet, "%q input %d (%s): result", rule, i, name)
						assert.Equal(t, errString(wantErr), errString(gotErr), "%q input %d (%s): err", rule, i, name)
						assert.Equal(
							t,
							errString(wantDebug),
							errString(ev.LastDebugErr()),
							"%q input %d (%s): debug err",
							rule,
							i,
							name,
						)
					}
					assert.Equal(t, wantRet, Evaluate(rule, input), "%q input %d: Evaluate", rule, i)
				}
			}
		})
	}
}

// versionRuleValues are rule versions; the overflowing ones are lexed as
// versions but rejected by semver.Make.
func versionRuleValues() []string {
	return []string{
		`1.2.3`, `0.0.0`, `10.20.30`, `1.2.3-alpha`, `1.2.3-alpha.1`, `1.2.3-0`, `1.2.3-rc.1+b`,
		`1.2.3+build.5`, `1.2.3+-`, `1.2.3--x`,
		`99999999999999999999.0.0`, `1.99999999999999999999.0`, `1.2.99999999999999999999`,
		`1.2.3-99999999999999999999`, `"1.2.3"`,
	}
}

// versionLeftValues are object values compared against rule versions.
func versionLeftValues() []any {
	return []any{
		"1.2.3", "1.2.4", "1.2.2", "0.0.0", "10.20.30", "2.0.0", "1.10.0",
		"1.2.3-alpha", "1.2.3-alpha.1", "1.2.3-beta", "1.2.3-0", "1.2.3-rc.1+x",
		"1.2.3+build.5", "1.2.3+other", "1.2.3+", "1.2.3+a..b", "1.2.3+a_b", "1.2.3+a+b",
		"v1.2.3", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03", "1.2.3-01", "", " 1.2.3", "1.2.3 ",
		"99999999999999999999.0.0", "1.2.99999999999999999999", "1..3", "1.2.", ".2.3", "1.2.3-", "x.y.z", "1.2.٣",
		5, 1.5, true, nil, []string{"1.2.3"}, testStringer{"1.2.3"},
	}
}

// deepMap returns depth nested maps: each level has "r", the next level,
// and "z", its depth.
func deepMap(depth int) map[string]any {
	m := map[string]any{"z": depth}
	if depth > 0 {
		m["r"] = deepMap(depth - 1)
	}

	return m
}

// randomRuleCount is the number of seeded random rules in the differential
// run (a tenth of it with -short).
const randomRuleCount = 2000

func fuzzAttrs() []string {
	return []string{"a", "b", "c", "x", "n", "s"}
}

func fuzzVals() []string {
	return []string{
		`1`, `-3`, `0`, `5`, `1e3`, `-2E+1`, `2.5`, `-0.5`, `1.0e2`, `1.5e999`,
		`"hello"`, `"HeLLo"`, `""`, `"A"`, `"a\"b"`, `"\u0041"`, `"1.2.3"`, `1.2.3`, `1.2.3-alpha+b`,
		`true`, `false`, `null`, `99999999999999999999`, `-99999999999999999999`,
	}
}

func fuzzOps() []string {
	return []string{"eq", "ne", "gt", "lt", "ge", "le", "co", "sw", "ew", "in", "==", "!=", ">", "<", ">=", "<="}
}

func genPath(r *rand.Rand) string {
	parts := make([]string, 1+r.Intn(4))
	for i := range parts {
		parts[i] = fuzzAttrs()[r.Intn(len(fuzzAttrs()))]
	}

	return strings.Join(parts, ".")
}

func genList(r *rand.Rand) string {
	var pool []string
	switch r.Intn(3) {
	case 0:
		pool = []string{"1", "2", "5", "0", "99999999999999999999"}
	case 1:
		pool = []string{"1.5", "2.5", "-0.5", "1.5e999", "1.0"}
	default:
		pool = []string{`"a"`, `"hello"`, `""`, `"x"`, `"\u0041"`}
	}
	el := make([]string, 1+r.Intn(4))
	for i := range el {
		el[i] = pool[r.Intn(len(pool))]
	}

	return "[" + strings.Join(el, ", ") + "]"
}

func genRule(r *rand.Rand, depth int) string {
	if depth <= 0 || r.Intn(3) == 0 {
		if r.Intn(5) == 0 {
			return genPath(r) + " pr"
		}
		v := fuzzVals()[r.Intn(len(fuzzVals()))]
		if r.Intn(3) == 0 {
			v = genList(r)
		}

		return genPath(r) + " " + fuzzOps()[r.Intn(len(fuzzOps()))] + " " + v
	}
	switch r.Intn(4) {
	case 0:
		return "(" + genRule(r, depth-1) + ")"
	case 1:
		return "not (" + genRule(r, depth-1) + ")"
	case 2:
		return genRule(r, depth-1) + " and " + genRule(r, depth-1)
	default:
		return genRule(r, depth-1) + " or " + genRule(r, depth-1)
	}
}

func genVal(r *rand.Rand, depth int) any {
	switch r.Intn(16) {
	case 0:
		return nil
	case 1:
		return r.Intn(6)
	case 2:
		return int64(r.Intn(6))
	case 3:
		return int32(r.Intn(6))
	case 4:
		return 2.5
	case 5:
		return "hello"
	case 6:
		return "1.2.3"
	case 7:
		return true
	case 8:
		return []int{1, 2}
	case 9:
		return testStringer{"HELLO"}
	case 10:
		return map[string]any(nil)
	case 11:
		return map[string]string{"a": "b"}
	case 12:
		return float32(1.5)
	}
	if depth <= 0 {
		return "leaf"
	}

	return genMap(r, depth-1)
}

func genMap(r *rand.Rand, depth int) map[string]any {
	m := map[string]any{}
	for _, a := range fuzzAttrs() {
		if r.Intn(3) != 0 {
			m[a] = genVal(r, depth)
		}
	}

	return m
}

func testProcessZeroValue(t *testing.T) {
	t.Helper()
	tests := []struct {
		name  string
		items map[string]any
	}{
		{name: "nil items"},
		{name: "items", items: map[string]any{"x": 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var wantErr error
			func() {
				v := NewJSONQueryVisitorImpl(tt.items)
				v.Visit(nil)
				wantErr = v.err
			}()
			ev := &Evaluator{}
			got, err := ev.Process(tt.items)
			assert.False(t, got)
			assert.Equal(t, errString(wantErr), errString(err))
			assert.NoError(t, ev.LastDebugErr())
		})
	}
}

func testProcessDoesNotLeakRuleConstants(t *testing.T) {
	t.Helper()
	tests := []struct {
		name   string
		rule   string
		bad    map[string]any
		mutate func(rule any)
		probe  map[string]any
		want   bool
	}{
		{
			name:   "ints",
			rule:   `x in [1, 2]`,
			bad:    map[string]any{"x": "str"},
			mutate: func(r any) { r.([]int)[0] = 42 },
			probe:  map[string]any{"x": 42},
		},
		{
			name:   "floats",
			rule:   `x in [1.5, 2.5]`,
			bad:    map[string]any{"x": "str"},
			mutate: func(r any) { r.([]float64)[0] = 42 },
			probe:  map[string]any{"x": 42.0},
		},
		{
			name:   "strings",
			rule:   `x in ["a", "b"]`,
			bad:    map[string]any{"x": 5},
			mutate: func(r any) { r.([]string)[0] = "zzz" },
			probe:  map[string]any{"x": "zzz"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ev, err := NewEvaluator(tt.rule)
			require.NoError(t, err)
			_, err = ev.Process(tt.bad)
			require.NoError(t, err)
			var ne *NestedError
			require.ErrorAs(t, ev.LastDebugErr(), &ne)
			tt.mutate(ne.Vals["rule_operand"])
			got, _ := ev.Process(tt.probe)
			assert.Equal(t, tt.want, got, "rule constant was mutated through the debug error")
		})
	}
}

func testProcessErrorIdentity(t *testing.T) {
	t.Helper()
	tests := []struct {
		name string
		rule string
	}{
		{name: "long overflow", rule: `x eq 99999999999999999999`},
		{name: "int list overflow", rule: `x in [1, 99999999999999999999]`},
		{name: "double list overflow", rule: `x in [1.5, 1.5e999]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ev, err := NewEvaluator(tt.rule)
			require.NoError(t, err)
			_, err1 := ev.Process(map[string]any{"x": 1})
			_, err2 := ev.Process(map[string]any{"x": 1})
			var ne1, ne2 *strconv.NumError
			require.ErrorAs(t, err1, &ne1)
			require.ErrorAs(t, err2, &ne2)
			require.NotSame(t, ne1, ne2, "the same error value was returned by two calls")
			ne1.Func = "mutated"
			assert.NotEqual(t, "mutated", ne2.Func, "errors share state")
		})
	}
}

func testProcessConcurrent(t *testing.T) {
	t.Helper()
	tests := []struct {
		name  string
		rule  string
		items map[string]any
		want  bool
	}{
		{name: "int in", rule: `y in [1, 2, 3]`, items: map[string]any{"y": 2}, want: true},
		{
			name:  "nested",
			rule:  `u.plan eq "pro" and u.age gt 18`,
			items: map[string]any{"u": map[string]any{"plan": "pro", "age": 31}},
			want:  true,
		},
		{name: "debug err", rule: `y in [1, 2, 3]`, items: map[string]any{"y": "s"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ev, err := NewEvaluator(tt.rule)
			require.NoError(t, err)
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					for range 500 {
						got, processErr := ev.Process(tt.items)
						assert.Equal(t, tt.want, got)
						_ = ev.LastDebugErr()
						assert.NoError(t, processErr)
					}
				})
			}
			wg.Wait()
		})
	}
}

func testProcessDoesNotLeakRuleConstantsViaOperandErrors(t *testing.T) {
	t.Helper()
	tests := []struct {
		name  string
		rule  string
		items map[string]any
		mut   func(val any)
		want  any
		// wantListTypeObj: the error's typeObj also holds the rule list
		wantListTypeObj bool
	}{
		{
			name:  "ints",
			rule:  `x eq [1, 2]`,
			items: map[string]any{"x": 1},
			mut:   func(v any) { v.([]int)[0] = 99 },
			want:  []int{1, 2},
		},
		{
			name:  "floats",
			rule:  `x eq [1.5, 2.5]`,
			items: map[string]any{"x": 1.0},
			mut:   func(v any) { v.([]float64)[0] = 99 },
			want:  []float64{1.5, 2.5},
		},
		{
			name:            "strings",
			rule:            `x eq ["a", "b"]`,
			items:           map[string]any{"x": "a"},
			mut:             func(v any) { v.([]string)[0] = "zzz" },
			want:            []string{"a", "b"},
			wantListTypeObj: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ev, err := NewEvaluator(tt.rule)
			require.NoError(t, err)
			_, err = ev.Process(tt.items)
			require.NoError(t, err)
			var ne *NestedError
			require.ErrorAs(t, ev.LastDebugErr(), &ne)
			var eio *InvalidOperandError
			require.ErrorAs(t, ne.Err, &eio)
			tt.mut(eio.Val)
			if typeObj := reflect.ValueOf(eio.typeObj); typeObj.Kind() == reflect.Slice {
				assert.NotEqual(t, reflect.ValueOf(ev.root.cmp.listOp).Pointer(), typeObj.Pointer(),
					"typeObj shares the compiled list's backing array")
				tt.mut(eio.typeObj)
			} else {
				require.False(t, tt.wantListTypeObj, "typeObj is %T, want the rule list", eio.typeObj)
			}
			tt.mut(ne.Vals["rule_operand"])
			assert.Equal(t, tt.want, ev.root.cmp.listOp, "compiled constant mutated")
			_, err = ev.Process(tt.items)
			require.NoError(t, err)
			var ne2 *NestedError
			require.ErrorAs(t, ev.LastDebugErr(), &ne2)
			var eio2 *InvalidOperandError
			require.ErrorAs(t, ne2.Err, &eio2)
			assert.Equal(t, tt.want, eio2.Val, "later debug error affected")
		})
	}
}

func testProcessConcurrentFirstCall(t *testing.T) {
	t.Helper()
	tests := []struct {
		name  string
		rule  string
		items map[string]any
		want  bool
	}{
		{name: "int in", rule: `y in [1, 2, 3]`, items: map[string]any{"y": 2}, want: true},
		{
			name:  "nested",
			rule:  `u.plan eq "pro" and u.age gt 18`,
			items: map[string]any{"u": map[string]any{"plan": "pro", "age": 31}},
			want:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for range 50 {
				ev, err := NewEvaluator(tt.rule)
				require.NoError(t, err)
				var wg sync.WaitGroup
				start := make(chan struct{})
				for range 16 {
					wg.Go(func() {
						<-start
						got, processErr := ev.Process(tt.items)
						assert.Equal(t, tt.want, got)
						assert.NoError(t, processErr)
					})
				}
				close(start)
				wg.Wait()
			}
		})
	}
}

func testProcessCompileFailureFallsBack(t *testing.T) {
	t.Helper()
	// compileQuery panics on an incomplete tree; every Process call must
	// then report what the visitor reports for that tree, not a
	// half-initialized evaluator.
	tests := []struct {
		name  string
		tree  antlr.ParseTree
		items map[string]any
	}{
		{name: "nil items", tree: &CompareExpContext{}},
		{name: "items", tree: &CompareExpContext{}, items: map[string]any{"x": 1}},
		{name: "unexpected tree node", tree: &QueryContext{}, items: map[string]any{"x": 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantRet, wantErr, wantDebug := visitorProcess(tt.tree, tt.items)
			ev := &Evaluator{tree: tt.tree}
			for call := range 2 {
				gotRet, gotErr := ev.Process(tt.items)
				assert.Equal(t, wantRet, gotRet, "call %d", call)
				assert.Equal(t, errString(wantErr), errString(gotErr), "call %d", call)
				assert.Equal(t, errString(wantDebug), errString(ev.LastDebugErr()), "call %d", call)
			}
			assert.Nil(t, ev.root, "failed compile must leave no root")
			assert.NotNil(t, ev.tree, "failed compile must keep the tree")
		})
	}
}

func testProcessDropsTreeAfterCompile(t *testing.T) {
	t.Helper()
	ev, err := NewEvaluator(`x eq 1`)
	require.NoError(t, err)
	require.NotNil(t, ev.tree, "NewEvaluator must only parse")
	require.Nil(t, ev.root, "NewEvaluator must only parse")
	got, err := ev.Process(map[string]any{"x": 1})
	require.NoError(t, err)
	assert.True(t, got)
	assert.Nil(t, ev.tree, "tree should be dropped once compiled")
	assert.NotNil(t, ev.root)
}

func testProcessVersionFastPath(t *testing.T) {
	t.Helper()
	tests := []struct {
		rule     string
		wantFast bool
	}{
		{rule: `v gt 1.2.0`, wantFast: true},
		{rule: `v == 1.2.3-alpha+b`, wantFast: true},
		{rule: `v ne 0.0.0`, wantFast: true},
		{rule: `v le 1.2.3+build`, wantFast: true},
		{rule: `v ge 1.0.0`, wantFast: true},
		{rule: `v < 2.0.0`, wantFast: true},
		{rule: `v eq 1.2.3`, wantFast: true},
		{rule: `v gt 99999999999999999999.0.0`},
		{rule: `v lt 1.2.3-99999999999999999999`},
		{rule: `v co 1.2.3`},
		{rule: `v in 1.2.3`},
	}
	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()
			// the differential versions group would skip a rule that does not parse
			ev, err := NewEvaluator(tt.rule)
			require.NoError(t, err)
			items := map[string]any{"v": "1.2.3"}
			want, wantErr := ev.Process(items)
			assert.Equal(t, tt.wantFast, ev.root.cmp.version != nil, "fast path")
			// the fast path must be what evaluates the rule: with apply
			// replaced, only the slow path would notice
			slow := ev.root.cmp.apply
			calledSlow := false
			ev.root.cmp.apply = func(l, r Operand) (bool, error) {
				calledSlow = true

				return slow(l, r)
			}
			got, err := ev.Process(items)
			assert.Equal(t, want, got)
			assert.Equal(t, errString(wantErr), errString(err))
			assert.Equal(t, tt.wantFast, !calledSlow, "evaluated by the fast path")
		})
	}
}

func testProcessStaleDebugErr(t *testing.T) {
	t.Helper()
	bad := map[string]any{"x": "s"}
	good := map[string]any{"x": 1}
	tests := []struct {
		name  string
		clear func(ev *Evaluator)
	}{
		{name: "clean call", clear: func(ev *Evaluator) { _, _ = ev.Process(good) }},
		{name: "reset", clear: func(ev *Evaluator) { _ = ev.Reset() }},
		{name: "panicking call", clear: func(ev *Evaluator) {
			ev.testHookPanic = func() { panic("p") }
			_, _ = ev.Process(good)
			ev.testHookPanic = nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ev, err := NewEvaluator(`x in [1, 2]`)
			require.NoError(t, err)
			for round := range 3 {
				_, _ = ev.Process(bad)
				require.Error(t, ev.LastDebugErr(), "round %d: want a debug error", round)
				tt.clear(ev)
				require.NoError(t, ev.LastDebugErr(), "round %d: stale debug error", round)
			}
		})
	}
}

// TestPanicClearsDebugErrStoredByOverlappingCall checks that a panicking call
// does not leave the debug error another call stored while it was running.
func TestPanicClearsDebugErrStoredByOverlappingCall(t *testing.T) {
	t.Parallel()
	ev, err := NewEvaluator(`x in [1, 2]`)
	require.NoError(t, err)
	ev.testHookPanic = func() {
		ev.testHookPanic = nil
		// stands in for an overlapping call that failed after this one started
		_, _ = ev.Process(map[string]any{"x": "not a number"})
		require.Error(t, ev.LastDebugErr(), "overlapping call should have stored a debug error")
		panic("p")
	}
	_, err = ev.Process(map[string]any{"x": 1})
	require.Error(t, err, "want the recovered panic as an error")
	assert.NoError(t, ev.LastDebugErr(), "stale debug error")
}

func TestParsePlainVersion(t *testing.T) {
	t.Parallel()

	literals := []string{
		"1.2.3", "0.0.0", "10.20.30", "18446744073709551615.0.1", "18446744073709551616.0.1",
		"1.2.3+b", "1.2.3+build.5", "1.2.3+-", "1.2.3+a-b.c", "1.2.3+", "1.2.3+a.", "1.2.3+.a", "1.2.3+a..b",
		"1.2.3+a+b", "1.2.3+a_b", "1.2.3+é", "1+a.2.3", "1.2+a.3",
		"1.2.3-alpha", "1.2.3-alpha+b", "1.2.3-", "1.2", "1", "", "1.2.3.4", "1..3", ".2.3", "1.2.",
		"01.2.3", "1.02.3", "1.2.03", "0.0.00", "v1.2.3", " 1.2.3", "1.2.3 ", "1.2.٣", "-1.2.3", "+1.2.3",
	}
	const randomInputs = 20000
	inputs := make([]string, 0, len(literals)+randomInputs)
	inputs = append(inputs, literals...)
	// seeded random strings over the characters that matter to the parser
	rnd := rand.New(rand.NewSource(1))
	const alphabet = "0123456789..++--aZ_"
	for range randomInputs {
		b := make([]byte, rnd.Intn(12))
		for j := range b {
			b[j] = alphabet[rnd.Intn(len(alphabet))]
		}
		inputs = append(inputs, string(b))
	}
	for _, in := range inputs {
		got, ok := parsePlainVersion(in)
		want, err := semver.Make(in)
		if !ok {
			continue
		}
		require.NoError(t, err, "%q: accepted but semver.Make fails", in)
		assert.Empty(t, want.Pre, "%q", in)
		assert.Equal(t, want.Major, got.Major, "%q", in)
		assert.Equal(t, want.Minor, got.Minor, "%q", in)
		assert.Equal(t, want.Patch, got.Patch, "%q", in)
		assert.Nil(t, got.Pre, "%q", in)
		assert.Nil(t, got.Build, "%q", in)
	}
	for _, in := range []string{"1.2.3", "0.0.0", "1.2.3+b.5"} {
		_, ok := parsePlainVersion(in)
		assert.True(t, ok, "%q: not parsed by the fast path", in)
	}
}

// testProcessListCapacity checks that compiled IN lists have no spare
// capacity: a stale list operand is appended to, which must copy it rather
// than write into the shared compiled list.
func testProcessListCapacity(t *testing.T) {
	t.Helper()
	tests := []struct {
		rule string
	}{
		{rule: `x in [1, 2, 3]`},
		{rule: `x in [1.5, 2.5, 3.5]`},
		{rule: `x in ["a", "b", "c"]`},
		{rule: `x in [1, 2, 3, 4, 5]`},
	}
	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()
			ev, err := NewEvaluator(tt.rule)
			require.NoError(t, err)
			_, _ = ev.Process(map[string]any{"x": 1})
			list := reflect.ValueOf(ev.root.cmp.listOp)
			require.Equal(t, reflect.Slice, list.Kind())
			assert.Equal(t, list.Len(), list.Cap(), "listOp %v", ev.root.cmp.listOp)
		})
	}
}

// testProcessConcurrentStaleList shares one evaluator whose rule leaves the
// compiled five-element list as the stale operand that the next list is
// appended to (the overflowing constant sets an error that skips resetting
// it). Built by append, an uncapped five-element list has room for the two
// appended elements, so without the cap the goroutines would write into the
// shared list. Run with -race.
func testProcessConcurrentStaleList(t *testing.T) {
	t.Helper()
	tests := []struct {
		name  string
		rule  string
		items map[string]any
	}{
		{
			name:  "ints",
			rule:  `a eq 99999999999999999999 or b in [1, 2, 3, 4, 5] or c in [6, 7]`,
			items: map[string]any{"a": 1, "b": 2, "c": 6},
		},
		{
			name:  "floats",
			rule:  `a eq 99999999999999999999 or b in [1.5, 2.5, 3.5, 4.5, 5.5] or c in [6.5, 7.5]`,
			items: map[string]any{"a": 1, "b": 2.5, "c": 4.5},
		},
		{
			name:  "strings",
			rule:  `a eq 99999999999999999999 or b in ["p", "q", "x", "y", "z"] or c in ["v", "w"]`,
			items: map[string]any{"a": 1, "b": "y", "c": "w"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tree, err := parseRule(tt.rule)
			require.NoError(t, err)
			wantRet, wantErr, wantDebug := visitorProcess(tree, tt.items)
			ev, err := NewEvaluator(tt.rule)
			require.NoError(t, err)
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					for range 200 {
						got, processErr := ev.Process(tt.items)
						if !assert.Equal(t, wantRet, got) ||
							!assert.Equal(t, errString(wantErr), errString(processErr)) {
							return
						}
					}
				})
			}
			wg.Wait()
			assert.Equal(t, errString(wantDebug), errString(ev.LastDebugErr()), "debug err")
		})
	}
}

func TestAttrStack(t *testing.T) {
	t.Parallel()

	// pushes and pops around the inline/spill boundary; after each step the
	// top and the spilled length must match a plain slice.
	tests := []struct {
		name string
		ops  []int // > 0: push that value, 0: pop, -1: clear
	}{
		{name: "inline only", ops: []int{1, 2, 3, 4, 0, 0, 5}},
		{name: "spill and pop back", ops: []int{1, 2, 3, 4, 5, 0, 6, 7, 8, 0, 0, 0, 0}},
		{name: "spill clear spill", ops: []int{1, 2, 3, 4, 5, 6, 7, 8, -1, 11, 12, 13, 14, 15, 16, 0, 17}},
		{name: "clear after pop to boundary", ops: []int{1, 2, 3, 4, 5, 0, -1, 1, 2, 3, 4, 9}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var s attrStack
			var want []any
			for i, op := range tt.ops {
				switch {
				case op > 0:
					s.push(op)
					want = append(want, op)
				case op == 0:
					s.pop()
					want = want[:len(want)-1]
				default:
					s.clear()
					want = want[:0]
				}
				wantMore := max(len(want)-attrStackInline, 0)
				require.Equal(t, len(want), s.n, "op %d: n", i)
				require.Len(t, s.more, wantMore, "op %d: len(more)", i)
				if s.n > 0 {
					require.Equal(t, want[len(want)-1], s.top(), "op %d: top", i)
				}
			}
		})
	}
}
