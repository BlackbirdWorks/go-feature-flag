package flagruntime_test

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

// Flag sets that have no flag never produce an event, in either direction: their removal does not wait in the
// queue of a consumer that is not reading.
func TestEmptyFlagSetChurnDoesNotGrowTheEventQueue(t *testing.T) {
	t.Parallel()

	const churn = 200

	harness := newRig(t, flagruntime.Hooks{}, flagruntime.WithChangeBuffer(1))
	harness.start()

	empty := obj(flagruntime.KindFlags, []byte("{}\n"))

	generation := func(gen int, temp string, end int) *flagruntime.Content {
		sets := []controlplane.FlagSet{fset("aa-fill", src("f0"), "key-fill"), fset(endSet, src("fe"), "key-end")}
		if temp != "" {
			sets = append(sets, fset(temp, src("empty"), "key-"+temp))
		}

		return content(uint64(gen), objs{
			"f0": flagsObj(gen, "x"), "fe": flagsObj(end, "x"), "empty": empty, setsName: setsObj(t, sets...),
		})
	}

	harness.feed(generation(1, "", 1))
	drain(t, harness, 2)

	// the fill flag set changes and its event fills the channel: nothing is read from now on
	for idx := range churn {
		harness.feed(generation(idx+2, fmt.Sprintf("tmp-%d", idx), 1))
	}

	harness.feed(generation(churn+2, "", 2))

	var seen []string

	for {
		change := harness.nextChange()
		seen = append(seen, change.FlagSet)

		if change.FlagSet == endSet {
			break
		}
	}

	// the fill flag set may be seen once or twice (one event can already sit in the channel); never a temporary one
	for _, name := range seen {
		assert.Contains(t, []string{"aa-fill", endSet}, name, "no event for the empty flag sets that came and went")
	}

	assert.LessOrEqual(t, len(seen), 3)
}

func TestLargeJSONIntegersAreKeptExactly(t *testing.T) {
	t.Parallel()

	const (
		first  = 9007199254740993
		second = 9007199254740995
	)

	jsonFlags := func(value int64) flagruntime.Object {
		data := []byte(fmt.Sprintf(`{"x": {"variations": {"v": %d}, "defaultRule": {"variation": "v"}}}`, value))

		return flagruntime.Object{
			Kind: flagruntime.KindFlags, Format: controlplane.FormatJSON, Bytes: data,
			Checksum: controlplane.ChecksumOf(data).Value,
		}
	}

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	sets := setsObj(t, fset("a", src("f1"), keyA))

	harness.feed(content(1, objs{"f1": jsonFlags(first), setsName: sets}))
	harness.nextChange()

	set := harness.holder.Load().FlagSets["a"]

	assert.EqualValues(t, int64(first), value(set, "x"), "the integer is not rounded to a float64")

	harness.feed(content(2, objs{"f1": jsonFlags(second), setsName: sets}))

	change := harness.nextChange()
	update, ok := change.Diff.Updated["x"]

	require.True(t, ok, "the next integer is a change, not the same rounded float")
	assert.EqualValues(t, int64(first), update.Before.GetVariationValue("v"))
	assert.EqualValues(t, int64(second), update.After.GetVariationValue("v"))
}

// The configuration of a vetoed round is not the baseline of pendingRestart.
func TestVetoedBootstrapIsNotTheBaseline(t *testing.T) {
	t.Parallel()

	var veto atomic.Bool

	veto.Store(true)

	harness := newRig(t, flagruntime.Hooks{BeforeSwap: func(*flagruntime.Runtime) error {
		if veto.Load() {
			return errRefused
		}

		return nil
	}})
	harness.start()

	const template = "version: 2\nauth:\n  adminKeys: [%s]\nserver:\n  listeners:\n    - {name: api, address: \"%s\"}\n"

	vetoed := harness.reload(bootstrapFromYAML(t, fmt.Sprintf(template, "k1", ":8080")))

	assert.Equal(t, flagruntime.CodeVetoed, vetoed.Code)

	veto.Store(false)

	// the first accepted configuration has another listener: it is the baseline, so nothing is pending
	accepted := harness.reload(bootstrapFromYAML(t, fmt.Sprintf(template, "k2", ":9090")))

	assert.Equal(t, flagruntime.OutcomeApplied, accepted.Outcome)
	assert.Empty(t, harness.holder.Load().Status.PendingRestart)

	harness.reload(bootstrapFromYAML(t, fmt.Sprintf(template, "k3", ":7070")))

	assert.Equal(t, []string{flagruntime.SettingServerListeners}, harness.holder.Load().Status.PendingRestart)
}
