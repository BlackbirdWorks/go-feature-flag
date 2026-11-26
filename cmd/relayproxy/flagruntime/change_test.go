package flagruntime_test

import (
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// countingMarshaler counts how often a flag is encoded.
type countingMarshaler struct{ calls *atomic.Int64 }

func (c countingMarshaler) MarshalJSON() ([]byte, error) {
	c.calls.Add(1)

	return []byte(`"counted"`), c.failure()
}

// failure is the error of the encoding: none, the type is only here to count.
func (countingMarshaler) failure() error { return nil }

func flagWithMetadata(metadata map[string]any) *flagdef.InternalFlag {
	return &flagdef.InternalFlag{Metadata: &metadata}
}

func mapID(m map[string]uint32) uintptr { return reflect.ValueOf(m).Pointer() }

// Each flag is encoded once, by the first call, and every later call answers from the memo: the same map, no
// allocation (measured in TestFlagSet_ViewAllocations). Many goroutines asking at once all get one answer.
func TestChange_EncodesEachFlagOnce(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64

	set := &flagruntime.FlagSet{Flags: map[string]*flagdef.InternalFlag{
		"a": flagWithMetadata(map[string]any{"c": countingMarshaler{calls: &calls}}),
		"b": flagWithMetadata(map[string]any{"c": countingMarshaler{calls: &calls}}),
	}}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		answers = map[uintptr]struct{}{}
		errs    []error
	)

	for range 8 {
		wg.Go(func() {
			change, err := set.Change()

			mu.Lock()
			answers[mapID(change.Flags)] = struct{}{}

			if err != nil {
				errs = append(errs, err)
			}

			mu.Unlock()
		})
	}

	wg.Wait()

	require.Empty(t, errs)

	assert.Len(t, answers, 1, "every caller got the one answer that was stored")

	first, err := set.Change()
	require.NoError(t, err)

	second, err := set.Change()
	require.NoError(t, err)

	assert.Equal(t, mapID(first.Flags), mapID(second.Flags), "the memo is returned")
	assert.Len(t, first.Flags, 2)
	assert.NotZero(t, first.Hash)

	// the racing first calls may each encode (the first answer to be stored wins), a later call never does
	settled := calls.Load()

	_, _ = set.Change()

	assert.Equal(t, settled, calls.Load(), "no encoding after the memo is set")
}

// A single call encodes each flag once, however many flags there are (the whole-table hash reuses the encodings).
func TestChange_FirstCallEncodesEachFlagOnce(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64

	set := &flagruntime.FlagSet{Flags: map[string]*flagdef.InternalFlag{
		"a": flagWithMetadata(map[string]any{"c": countingMarshaler{calls: &calls}}),
		"b": flagWithMetadata(map[string]any{"c": countingMarshaler{calls: &calls}}),
		"c": flagWithMetadata(map[string]any{"c": countingMarshaler{calls: &calls}}),
	}}

	_, err := set.Change()
	require.NoError(t, err)

	assert.EqualValues(t, 3, calls.Load(), "three flags, three encodings")
}

// A flag that has no JSON encoding makes Change fail, and the failure is the answer of every later call as well.
func TestChange_EncodingErrorIsMemoized(t *testing.T) {
	t.Parallel()

	set := &flagruntime.FlagSet{Flags: map[string]*flagdef.InternalFlag{
		"good": flagWithMetadata(map[string]any{"a": 1}),
		"nan":  flagWithMetadata(map[string]any{"a": math.NaN()}),
	}}

	_, err := set.Change()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"nan"`)

	_, again := set.Change()
	require.Error(t, again)
	assert.Equal(t, err, again)
}

// A flag set that a later generation carries over keeps its memo (the same map); a flag set that is rebuilt computes
// its own, with the hashes of its new flags.
func TestChange_MemoFollowsThePointer(t *testing.T) {
	t.Parallel()

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	objects := func(valueA int) objs {
		return objs{
			"fa":     flagsObj(valueA, "f"),
			"fb":     flagsObj(2, "g"),
			setsName: setsObj(t, fset("a", src("fa"), keyA), fset("b", src("fb"), keyB)),
		}
	}

	harness.feed(content(1, objects(1)))

	run1 := harness.holder.Load()
	changeA1, err := run1.FlagSets["a"].Change()
	require.NoError(t, err)

	changeB1, err := run1.FlagSets["b"].Change()
	require.NoError(t, err)

	harness.feed(content(2, objects(10)))

	run2 := harness.holder.Load()

	require.Same(t, run1.FlagSets["b"], run2.FlagSets["b"], "the unchanged flag set is carried over")

	changeB2, err := run2.FlagSets["b"].Change()
	require.NoError(t, err)
	assert.Equal(t, mapID(changeB1.Flags), mapID(changeB2.Flags), "its memo is carried with it")

	require.NotSame(t, run1.FlagSets["a"], run2.FlagSets["a"])

	changeA2, err := run2.FlagSets["a"].Change()
	require.NoError(t, err)
	assert.NotEqual(t, changeA1.Hash, changeA2.Hash, "the rebuilt flag set has the hashes of its new flags")
	assert.NotEqual(t, changeA1.Flags["f"], changeA2.Flags["f"])
	assert.NotEqual(t, mapID(changeA1.Flags), mapID(changeA2.Flags))
}
