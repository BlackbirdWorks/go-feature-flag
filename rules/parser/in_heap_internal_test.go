package parser

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// retainedHeapKiB reports the heap (KiB) kept by an evaluator after one Process
// call and three GCs, and the evaluator itself. It reads process-wide memory
// statistics, so it is only meaningful in a benchmark, which runs alone.
func retainedHeapKiB(tb testing.TB, rule string) (float64, *Evaluator) {
	tb.Helper()
	runtime.GC()

	var memBefore, memAfter runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	eval, err := NewEvaluator(rule)
	require.NoError(tb, err)
	_, err = eval.Process(map[string]any{})
	require.NoError(tb, err)

	for range 3 {
		runtime.GC()
	}

	runtime.ReadMemStats(&memAfter)

	return float64(int64(memAfter.HeapAlloc)-int64(memBefore.HeapAlloc)) / 1024, eval
}

// TestInSetSize checks that a long IN list is held as a hash set of the
// expected kind and that the set stays compact: the bytes of the packed string
// list plus its index (strSet), or of the index alone (intSet; the ints
// themselves are the list the node keeps anyway), per element. The bounds follow
// from the layout: ends and slots are uint32, the table has 1.5 to 3 slots per
// element.
func TestInSetSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		rule           string
		n              int
		wantStrSet     bool
		wantIntSet     bool
		wantMaxPerElem float64
	}{
		{name: "str1k", rule: "s in " + seqStrs(1000), n: 1000, wantStrSet: true, wantMaxPerElem: 30},
		{name: "int1k", rule: "n in " + seqInts(1000), n: 1000, wantIntSet: true, wantMaxPerElem: 12},
		{name: "str300k", rule: "s in " + seqStrs(300000), n: 300000, wantStrSet: true, wantMaxPerElem: 30},
		{name: "int300k", rule: "n in " + seqInts(300000), n: 300000, wantIntSet: true, wantMaxPerElem: 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ev, err := NewEvaluator(tt.rule)
			require.NoError(t, err)
			_, err = ev.Process(map[string]any{})
			require.NoError(t, err)
			require.NotNil(t, ev.root)
			cmp := ev.root.cmp
			assert.Equal(t, tt.wantStrSet, cmp.strSet != nil, "string hash set")
			assert.Equal(t, tt.wantIntSet, cmp.intSet != nil, "int hash set")

			var bytes, slots int

			switch {
			case cmp.strSet != nil:
				slots = len(cmp.strSet.tab)
				bytes = len(cmp.strSet.data) + 4*len(cmp.strSet.ends) + 4*slots
				assert.Nil(t, cmp.listOp, "the packed set replaces the list")
			case cmp.intSet != nil:
				slots = len(cmp.intSet.tab)
				bytes = 4 * slots
			}

			assert.GreaterOrEqual(t, slots, tt.n*3/2, "at least 1.5 slots per element")
			assert.LessOrEqual(t, slots, 4*tt.n, "at most 4 slots per element (power of two over 1.5n)")
			assert.LessOrEqual(t, float64(bytes)/float64(tt.n), tt.wantMaxPerElem, "bytes per element")
		})
	}
}

// BenchmarkInSetHeap reports the heap a compiled list keeps after NewEvaluator, one
// Process call and three GCs. The figure includes the rule text if the
// evaluator retains it.
func BenchmarkInSetHeap(b *testing.B) {
	benches := []struct {
		name string
		rule string
	}{
		{name: "str1k", rule: "s in " + seqStrs(1000)},
		{name: "int1k", rule: "n in " + seqInts(1000)},
		{name: "str300k", rule: "s in " + seqStrs(300000)},
		{name: "int300k", rule: "n in " + seqInts(300000)},
	}
	for _, bench := range benches {
		b.Run(bench.name, func(b *testing.B) {
			var (
				kib  float64
				eval *Evaluator
			)
			for range b.N {
				kib, eval = retainedHeapKiB(b, bench.rule)
			}

			b.ReportMetric(kib, "retained-KiB")
			b.ReportMetric(float64(len(bench.rule))/1024, "rule-text-KiB")
			runtime.KeepAlive(eval)
		})
	}
}
