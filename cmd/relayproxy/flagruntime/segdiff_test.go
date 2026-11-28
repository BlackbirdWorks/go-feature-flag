package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

const docBetaThree = segHeader +
	"  - name: beta\n    members: [alice, bob, carol]\n  - name: unused\n    members: [x]\n"

// A segment source that is detached from a flag set, with the flag file unchanged, changes the evaluations of the
// flags that use the segment: they are reported as updated and the hashes of /v1/flag/change change.
func TestDetachingASegmentSourceIsAChangeOfTheFlagsThatUseIt(t *testing.T) {
	t.Parallel()

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	all := objs{
		"f1":      obj(flagruntime.KindFlags, []byte(betaFlagYAML)),
		"f2":      flagsObj(1, "z"),
		segObject: segObj(docBetaUnused),
		setsName:  withSegmentSources(t),
	}

	first := harness.feed(content(1, all))
	for range first.Events {
		harness.nextChange()
	}

	before, err := harness.holder.Load().FlagSets["a"].Change()
	require.NoError(t, err)

	detached := setsObj(t,
		controlplane.FlagSet{Name: "a", Sources: src("f1"), APIKeys: []string{keyA}},
		fset("b", src("f2"), keyB),
	)

	second := harness.feed(content(2, all.with(objs{setsName: detached})))
	require.Equal(t, 1, second.Events, "only the flag set that lost its segments has a change")

	change := harness.nextChange()
	assert.Equal(t, "a", change.FlagSet)
	assert.ElementsMatch(t, []string{"beta-flag", "staff-flag"}, sortedKeys(change.Diff.Updated))
	assert.Empty(t, change.Diff.Added)
	assert.Empty(t, change.Diff.Deleted)

	after, err := harness.holder.Load().FlagSets["a"].Change()
	require.NoError(t, err)

	assert.NotEqual(t, before.Hash, after.Hash, "the hash of the flag set")
	assert.NotEqual(t, before.Flags["beta-flag"], after.Flags["beta-flag"], "the hash of a flag that used the segment")
}

// While the consumer is stalled, a membership change that is undone before it is read is no change, and one that
// stays is one update from the first state to the last.
func TestQueuedMembershipChangesCompose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		third       string
		wantUpdated []string
		wantNone    bool
	}{
		{name: "changed then restored is nothing", third: docBetaUnused, wantNone: true},
		{name: "changed then changed again is one update", third: docBetaThree, wantUpdated: []string{"beta-flag"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			harness := newRig(t, flagruntime.Hooks{}, flagruntime.WithChangeBuffer(1))
			harness.start()

			all := objs{
				"f1":      obj(flagruntime.KindFlags, []byte(betaFlagYAML)),
				"f2":      flagsObj(1, "z"),
				segObject: segObj(docBetaUnused),
				setsName:  withSegmentSources(t),
			}

			// the first generation fills the channel with the event of a; b stays pending
			harness.feed(content(1, all))
			harness.nextEmitted()

			harness.feed(content(2, all.with(objs{segObject: segObj(docBetaTwo)})))
			harness.feed(content(3, all.with(objs{segObject: segObj(tt.third)})))

			wantEvents := 3
			if tt.wantNone {
				wantEvents = 2
			}

			events := drain(t, harness, wantEvents)

			var merged *flagruntime.Change

			for i := range events {
				if events[i].FlagSet == "a" && events[i].Generation > 1 {
					merged = &events[i]
				}
			}

			if tt.wantNone {
				assert.Nil(t, merged, "a membership change that is undone has no event")

				return
			}

			require.NotNil(t, merged)
			assert.Equal(t, tt.wantUpdated, nilIfEmpty(sortedKeys(merged.Diff.Updated)))
		})
	}
}
