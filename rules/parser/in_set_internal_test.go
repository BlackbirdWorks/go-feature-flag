package parser

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	aBIn       = "a.b in "
	stringType = "string"
	nIn        = "n in "
	notSIn     = "not (s in "
	sIn        = "s in "
	uD         = "u%d"
	xIn        = "x in "
)

func countSets(curr *node) int {
	if curr == nil {
		return 0
	}

	c := 0
	if curr.cmp != nil && (curr.cmp.strSet != nil || curr.cmp.intSet != nil) {
		c++
	}

	return c + countSets(curr.left) + countSets(curr.right)
}

// seqStrs returns the list literal ["u0", ..., "u<n-1>"].
func seqStrs(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("%q", fmt.Sprintf(uD, i))
	}

	return "[" + strings.Join(parts, ", ") + "]"
}

// seqInts returns the list literal [0, ..., n-1].
func seqInts(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = strconv.Itoa(i)
	}

	return "[" + strings.Join(parts, ", ") + "]"
}

func randStrList(r *rand.Rand, count int) string {
	pool := []string{
		"a",
		"A",
		"u1",
		"U1",
		"",
		"ß",
		"é",
		"é",
		versionOneTwoThree,
		"5",
		fixtureTrue,
		null2,
		"x y",
		`q\"q`,
		"日本", //nolint:gosmopolitan // non-ASCII input tests UTF-8 handling
		"İ",
		"i",
	}

	parts := make([]string, count)
	for i := range parts {
		s := fmt.Sprintf(uD, r.Intn(count*2+1))
		if r.Intn(3) == 0 {
			s = pool[r.Intn(len(pool))]
		}

		parts[i] = `"` + s + `"` // pool entries containing \" are already escaped
	}

	return "[" + strings.Join(parts, ", ") + "]"
}

// randIntList builds an int list; with bad set, one element overflows int so
// the compiled list is partial and carries a parse error (no set is used).
func randIntList(r *rand.Rand, count int, bad bool) string {
	parts := make([]string, count)
	for i := range parts {
		switch r.Intn(6) {
		case 0:
			parts[i] = "0"
		case 1:
			parts[i] = strconv.Itoa(r.Intn(10)) // the grammar has no negative list ints
		default:
			parts[i] = strconv.Itoa(r.Intn(count * 2))
		}
	}

	if bad && count > 2 {
		parts[r.Intn(count)] = overflowInteger
	}

	return "[" + strings.Join(parts, ", ") + "]"
}

type kv = map[string]any

// TestInSet checks IN / NOT IN results on lists above the set threshold
// against hand-written expectations and against the original visitor, which
// never uses the set.
func TestInSet(t *testing.T) {
	t.Parallel()

	strs, ints := seqStrs(100), seqInts(100)

	tests := []struct {
		input   kv
		name    string
		rule    string
		want    bool
		wantSet bool // a hash set is expected to be compiled
	}{
		{name: "string hit", rule: sIn + strs, input: kv{"s": "u50"}, want: true, wantSet: true},
		{name: "string first", rule: sIn + strs, input: kv{"s": "u0"}, want: true, wantSet: true},
		{name: "string last", rule: sIn + strs, input: kv{"s": "u99"}, want: true, wantSet: true},
		{name: "string miss", rule: sIn + strs, input: kv{"s": "u100"}, want: false, wantSet: true},
		{name: "string is case sensitive", rule: sIn + strs, input: kv{"s": "U50"}, want: false, wantSet: true},
		{name: "string empty", rule: sIn + strs, input: kv{"s": ""}, want: false, wantSet: true},
		{name: "string upper-case operator", rule: "s IN " + strs, input: kv{"s": "u7"}, want: true, wantSet: true},
		{name: "string stringer", rule: sIn + strs, input: kv{"s": testStringer{"u7"}}, want: true, wantSet: true},
		{name: "string wrong type", rule: sIn + strs, input: kv{"s": 7}, want: false, wantSet: true},
		{name: "string nil value", rule: sIn + strs, input: kv{"s": nil}, want: false, wantSet: true},
		{name: "string missing attribute", rule: sIn + strs, input: kv{"other": "u1"}, want: false, wantSet: true},
		{name: "string nil input", rule: sIn + strs, input: nil, want: false, wantSet: true},
		{name: "string nested hit", rule: aBIn + strs, input: kv{"a": kv{"b": "u3"}}, want: true, wantSet: true},
		{name: "string nested miss", rule: aBIn + strs, input: kv{"a": kv{"b": "zz"}}, want: false, wantSet: true},
		{name: "string nested not object", rule: aBIn + strs, input: kv{"a": "u3"}, want: false, wantSet: true},
		{name: "string not in hit", rule: notSIn + strs + ")", input: kv{"s": "u50"}, want: false, wantSet: true},
		{name: "string not in miss", rule: notSIn + strs + ")", input: kv{"s": "zz"}, want: true, wantSet: true},
		{name: "string not in missing", rule: notSIn + strs + ")", input: kv{}, want: true, wantSet: true},
		{
			name:    "string in or other",
			rule:    sIn + strs + " or n eq 1",
			input:   kv{"s": "zz", "n": 1},
			want:    true,
			wantSet: true,
		},
		{
			name:    "string in and other",
			rule:    sIn + strs + " and n eq 1",
			input:   kv{"s": "u1", "n": 2},
			want:    false,
			wantSet: true,
		},
		{name: "int hit", rule: nIn + ints, input: kv{"n": 50}, want: true, wantSet: true},
		{name: "int first", rule: nIn + ints, input: kv{"n": 0}, want: true, wantSet: true},
		{name: "int last", rule: nIn + ints, input: kv{"n": 99}, want: true, wantSet: true},
		{name: "int miss", rule: nIn + ints, input: kv{"n": 100}, want: false, wantSet: true},
		{name: "int negative", rule: nIn + ints, input: kv{"n": -1}, want: false, wantSet: true},
		{name: "int float value", rule: nIn + ints, input: kv{"n": 5.0}, want: false, wantSet: true},
		{name: "int int64 value", rule: nIn + ints, input: kv{"n": int64(5)}, want: false, wantSet: true},
		{name: "int string value", rule: nIn + ints, input: kv{"n": "5"}, want: false, wantSet: true},
		{name: "int bool value", rule: nIn + ints, input: kv{"n": true}, want: false, wantSet: true},
		{name: "int nil value", rule: nIn + ints, input: kv{"n": nil}, want: false, wantSet: true},
		{name: "int missing attribute", rule: nIn + ints, input: kv{}, want: false, wantSet: true},
		{name: "int nested hit", rule: "a.n in " + ints, input: kv{"a": kv{"n": 9}}, want: true, wantSet: true},
		{name: "int not in hit", rule: "not (n in " + ints + ")", input: kv{"n": 9}, want: false, wantSet: true},
		{name: "int not in miss", rule: "not (n in " + ints + ")", input: kv{"n": 900}, want: true, wantSet: true},
		{
			name:    "int duplicates",
			rule:    "n in [" + strings.Repeat("3, ", 99) + "4]",
			input:   kv{"n": 4},
			want:    true,
			wantSet: true,
		},
		{
			name:    "float list never uses a set",
			rule:    "n in [" + strings.Repeat("1.0, ", 99) + "2.0]",
			input:   kv{"n": 2.0},
			want:    true,
			wantSet: false,
		},
		{name: "short string list", rule: sIn + seqStrs(5), input: kv{"s": "u4"}, want: true, wantSet: false},
		{name: "short int list", rule: nIn + seqInts(5), input: kv{"n": 4}, want: true, wantSet: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			eval, err := NewEvaluator(tt.rule)
			require.NoError(t, err)
			// the visitor must run first: it reads the parse tree that Process
			// compiles away. The second pass reuses the compiled rule.
			wantRet, wantErr, wantDebug := visitorProcess(eval.tree, tt.input)
			for pass := range 2 {
				got, processErr := eval.Process(tt.input)
				if tt.want {
					require.NoError(t, processErr, "pass %d", pass)
				}

				require.Equal(t, tt.want, got, "pass %d (err %v)", pass, processErr)
				require.Equal(t, wantRet, got, "pass %d: visitor result", pass)
				require.Equal(t, errString(wantErr), errString(processErr), "pass %d: visitor err", pass)
				require.Equal(
					t,
					errString(wantDebug),
					errString(eval.LastDebugErr()),
					"pass %d: visitor debug err",
					pass,
				)
			}

			require.Equal(t, tt.wantSet, countSets(eval.root) > 0, "hash set compiled")
		})
	}
}

// TestInSetThreshold checks the switch from linear scan to hash set at
// inSetMinLen, and that both sides of the boundary give the same answers.
func TestInSetThreshold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		n       int
		wantSet bool
	}{
		{"one", 1, false},
		{"below", inSetMinLen - 1, false},
		{"at", inSetMinLen, true},
		{"above", inSetMinLen + 1, true},
	}
	for _, kind := range []string{stringType, intType} {
		for _, tt := range tests {
			t.Run(kind+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				var (
					rule              string
					first, last, miss any
				)
				if kind == stringType {
					rule, first, last, miss = xIn+seqStrs(
						tt.n,
					), "u0", fmt.Sprintf(
						uD,
						tt.n-1,
					), fmt.Sprintf(
						uD,
						tt.n,
					)
				} else {
					rule, first, last, miss = xIn+seqInts(tt.n), 0, tt.n-1, tt.n
				}

				eval, err := NewEvaluator(rule)
				require.NoError(t, err)

				for _, probe := range []struct {
					v    any
					want bool
				}{{first, true}, {last, true}, {miss, false}} {
					got, processErr := eval.Process(kv{"x": probe.v})
					require.NoError(t, processErr, "x=%v", probe.v)
					require.Equal(t, probe.want, got, "x=%v", probe.v)
				}

				require.Equal(t, tt.wantSet, countSets(eval.root) > 0, "hash set compiled")
			})
		}
	}
}

// TestInSetDifferential compares compiled evaluation with the original
// visitor over seeded random lists and a spread of awkward inputs: results,
// errors and debug errors must be identical.
func TestInSetDifferential(t *testing.T) {
	t.Parallel()

	vals := []any{
		"u1",
		"U1",
		"u3",
		"a",
		"A",
		"",
		"é",
		"é",
		"ß",
		"SS",
		"5",
		versionOneTwoThree,
		null2,
		"i",
		"İ",
		"日本", //nolint:gosmopolitan // non-ASCII input tests UTF-8 handling
		0,
		1,
		3,
		-1,
		5,
		15,
		int64(3),
		int32(3),
		uint(3),
		3.0,
		2.0,
		0.0,
		math.Copysign(0, -1),
		math.NaN(),
		math.Inf(1),
		float32(3),
		true,
		false,
		nil,
		[]string{"u1"},
		[]int{3},
		kv{"a": 1},
		testStringer{"u1"},
		testStringer{"U1"},
		fmt.Stringer(nil),
		"\xff",
	}
	inputs := make([]kv, 0, 2+len(vals)+3)

	inputs = append(inputs, nil, kv{})
	for _, v := range vals {
		inputs = append(inputs, kv{"s": v, "n": v, "u": kv{"s": v, "n": v}})
	}

	inputs = append(inputs, kv{"n": 3, "s": "u1"}, kv{"n": "x", "s": 3}, kv{"other": 1})

	for _, size := range []int{1, 7, inSetMinLen - 1, inSetMinLen, inSetMinLen + 1, 200} {
		t.Run(fmt.Sprintf("size=%d", size), func(t *testing.T) {
			t.Parallel()

			r := rand.New(rand.NewSource(int64(7 + size)))
			rules := make([]string, 0, 39)

			for range 2 {
				strList, intList, intBad := randStrList(
					r,
					size,
				), randIntList(
					r,
					size,
					false,
				), randIntList(
					r,
					size,
					true,
				)
				rules = append(rules,
					sIn+strList, "s IN "+strList, "u.s in "+strList,
					notSIn+strList+")", "NOT (n in "+intList+")",
					nIn+intList, "u.n in "+intList, nIn+intBad, nIn+intBad+" or s in "+strList,
					sIn+strList+" and n in "+intList, "(s in "+strList+") or (n in "+intBad+")",
					sIn+strList+" or s in "+randStrList(r, size), "missing in "+strList, "missing in "+intList,
					sIn+strList+" or n in "+intList+" or s eq \"u1\"",
					nIn+intList+" or n in "+randIntList(r, size, false),
					sIn+strList+" or s pr and not (n in "+intList+")",
				)
			}

			rules = append(rules,
				`n in [1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0,9.0]`, `n in [1,2,3,4,5,6,7,8,9,1.5]`,
				`s in ["a","b","c","d","e","f","g","h",1]`, `n in [0,0,0,0,0,0,0,0,0]`, `n in [-0,1,2,3,4,5,6,7,8]`)

			sets := 0

			for _, rule := range rules {
				repeated, err := NewEvaluator(rule)
				if err != nil {
					continue // invalid mixed lists are rejected at parse time
				}

				tree := repeated.tree
				for i, input := range inputs {
					wantRet, wantErr, wantDebug := visitorProcess(tree, input)

					fresh := &Evaluator{rule: rule, tree: tree}
					for name, eval := range map[string]*Evaluator{"fresh": fresh, "repeated": repeated} {
						gotRet, gotErr := eval.Process(input)
						assert.Equal(t, wantRet, gotRet, "%.80q input %d %v (%s): result", rule, i, input, name)
						assert.Equal(
							t,
							errString(wantErr),
							errString(gotErr),
							"%.80q input %d %v (%s): err",
							rule,
							i,
							input,
							name,
						)
						assert.Equal(
							t,
							errString(wantDebug),
							errString(eval.LastDebugErr()),
							"%.80q input %d %v (%s): debug err",
							rule,
							i,
							input,
							name,
						)
					}
				}

				sets += countSets(repeated.root)
			}

			if size >= inSetMinLen {
				require.NotZero(t, sets, "no hash set was built: test is vacuous")
			} else {
				require.Zero(t, sets, "hash sets built below the threshold")
			}
		})
	}
}

// TestInSetConcurrent evaluates one compiled evaluator from many goroutines;
// run with -race to check that the set is read-only after compile.
func TestInSetConcurrent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		val  func(i int) any
		want func(i int) bool
		name string
		rule string
	}{
		{
			name: stringType,
			rule: sIn + seqStrs(300),
			val:  func(i int) any { return fmt.Sprintf(uD, i%600) },
			want: func(i int) bool { return i%600 < 300 },
		},
		{
			name: intType,
			rule: sIn + seqInts(300),
			val:  func(i int) any { return i % 600 },
			want: func(i int) bool { return i%600 < 300 },
		},
		{
			name: "string not in",
			rule: notSIn + seqStrs(300) + ")",
			val:  func(i int) any { return fmt.Sprintf(uD, i%600) },
			want: func(i int) bool { return i%600 >= 300 },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			eval, err := NewEvaluator(tt.rule)
			require.NoError(t, err)

			var wg sync.WaitGroup
			for range 16 {
				wg.Go(func() {
					for i := range 1000 {
						got, _ := eval.Process(kv{"s": tt.val(i)})
						if !assert.Equal(t, tt.want(i), got, "i=%d", i) {
							return
						}
					}
				})
			}

			wg.Wait()
		})
	}
}
