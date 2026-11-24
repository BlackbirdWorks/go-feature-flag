package parser

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// retainedHeap reports the heap (KiB) kept by an evaluator after one Process
// call and three GCs, and the evaluator itself. The figure is approximate
// when other tests allocate at the same time; it is logged, never asserted.
func retainedHeap(t *testing.T, rule string) (float64, *Evaluator) {
	t.Helper()
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	ev, err := NewEvaluator(rule)
	require.NoError(t, err)
	_, err = ev.Process(map[string]any{})
	require.NoError(t, err)
	for range 3 {
		runtime.GC()
	}
	runtime.ReadMemStats(&m1)

	return float64(int64(m1.HeapAlloc)-int64(m0.HeapAlloc)) / 1024, ev
}

// TestInSetHeap logs the heap a compiled list keeps after NewEvaluator, one
// Process call and three GCs (run with -v), and checks that the long list is
// held as the expected hash set. The figure includes the rule text if the
// evaluator retains it.
func TestInSetHeap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		rule       string
		wantStrSet bool
		wantIntSet bool
	}{
		{name: "str1k", rule: "s in " + seqStrs(1000), wantStrSet: true},
		{name: "int1k", rule: "n in " + seqInts(1000), wantIntSet: true},
		{name: "str300k", rule: "s in " + seqStrs(300000), wantStrSet: true},
		{name: "int300k", rule: "n in " + seqInts(300000), wantIntSet: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			kib, ev := retainedHeap(t, tt.rule)
			t.Logf("%s retained heap %.0f KiB (rule text %.0f KiB)", tt.name, kib, float64(len(tt.rule))/1024)
			require.NotNil(t, ev.root)
			assert.Equal(t, tt.wantStrSet, ev.root.cmp.strSet != nil, "string hash set")
			assert.Equal(t, tt.wantIntSet, ev.root.cmp.intSet != nil, "int hash set")
			runtime.KeepAlive(ev)
		})
	}
}
