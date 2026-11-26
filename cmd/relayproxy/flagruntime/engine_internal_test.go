package flagruntime

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// Flag sets that come and go, with or without a consumer that reads, leave no state behind: the tracking of
// the flag sets the consumer knows is bounded by the live ones.
func TestEngineTrackingStaysBoundedUnderCreateAndWithdrawCycles(t *testing.T) {
	t.Parallel()

	const cycles = 10000

	tests := []struct {
		name     string
		consumer bool
	}{
		{name: "a consumer that reads every event", consumer: true},
		{name: "a consumer that reads nothing", consumer: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := flagdef.NewService()
			t.Cleanup(svc.Close)

			eng := NewEngine(nil, svc, NewHolder())
			flags := Object{Kind: KindFlags, Format: controlplane.FormatYAML, Bytes: []byte(internalFlags)}
			stay := controlplane.FlagSet{Name: stayName, Sources: []string{"f1"}, APIKeys: []string{keyStay}}
			generation := uint64(0)

			round := func(sets ...controlplane.FlagSet) {
				generation++

				eng.round(context.Background(), &Content{
					Generation: generation, Objects: map[string]Object{"f1": flags, setsKey: internalDoc(t, sets...)},
				}, nil)

				for tt.consumer && eng.queue.front() != nil {
					eng.delivered(eng.queue.front())
				}
			}

			round(stay)

			for cycle := range cycles {
				name := fmt.Sprintf("tmp-%d", cycle)

				round(stay, controlplane.FlagSet{Name: name, Sources: []string{"f1"}, APIKeys: []string{"key-" + name}})
				round(stay)
			}

			require.Equal(t, uint64(2*cycles+1), generation)

			// the flag set stayName was announced at the start; with no consumer its event still waits
			assert.LessOrEqual(t, len(eng.announced), 2)
			assert.LessOrEqual(t, len(eng.queue.byName), 2)
			assert.Contains(t, eng.announced, stayName)
			assert.NotContains(t, eng.announced, fmt.Sprintf("tmp-%d", cycles-1))
		})
	}
}

// checkAnnounced asserts the invariant of the tracking of the flag sets that the consumer knows: a name is known
// when its flag set is live or its Removed event waits, and every pending change is for a known name.
func checkAnnounced(t *testing.T, eng *Engine) {
	t.Helper()

	for name := range eng.announced {
		_, live := eng.cur.FlagSets[name]
		pending := eng.queue.byName[name]

		assert.True(t, live || (pending != nil && pending.change.Removed),
			"%s is known but neither live nor waiting to be removed", name)
	}

	for name := range eng.queue.byName {
		assert.Contains(t, eng.announced, name, "%s has a pending change and is not known", name)
	}
}

// The sequences of the event tables, driven round by round without a consumer goroutine, with the invariant
// checked after every step.
func TestAnnouncedInvariantHoldsAtEveryStep(t *testing.T) {
	t.Parallel()

	type step struct {
		flags   string // the flags file of the flag set tmpName; empty means the flag set is withdrawn
		deliver bool   // the consumer reads everything that waits after this step
	}

	const (
		withFlag = internalFlags
		empty    = "{}\n"
	)

	tests := []struct {
		name        string
		steps       []step
		wantPending []string
		wantKnown   []string
	}{
		{
			name:  "gains a flag, loses it, is withdrawn before any read: no event at all",
			steps: []step{{flags: empty}, {flags: withFlag}, {flags: empty}, {}},
		},
		{
			name:        "created with a flag and read: it is known and live",
			steps:       []step{{flags: withFlag, deliver: true}},
			wantKnown:   []string{tmpName},
			wantPending: nil,
		},
		{
			name:      "created, read, then withdrawn and read: the removal clears it",
			steps:     []step{{flags: withFlag, deliver: true}, {deliver: true}},
			wantKnown: nil,
		},
		{
			name:        "created, read, then withdrawn: the removal waits and the name stays known",
			steps:       []step{{flags: withFlag, deliver: true}, {}},
			wantKnown:   []string{tmpName},
			wantPending: []string{tmpName},
		},
		{
			name:        "created and read, flag lost and regained before the next read",
			steps:       []step{{flags: withFlag, deliver: true}, {flags: empty}, {flags: withFlag}},
			wantKnown:   []string{tmpName},
			wantPending: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := flagdef.NewService()
			t.Cleanup(svc.Close)

			eng := NewEngine(nil, svc, NewHolder())
			stay := controlplane.FlagSet{Name: stayName, Sources: []string{anchorName}, APIKeys: []string{keyStay}}
			anchor := Object{Kind: KindFlags, Format: controlplane.FormatYAML, Bytes: []byte(internalFlags)}

			// the "stay" flag set is known from the start and never changes
			eng.round(context.Background(), &Content{
				Generation: 1, Objects: map[string]Object{anchorName: anchor, setsKey: internalDoc(t, stay)},
			}, nil)

			for front := eng.queue.front(); front != nil; front = eng.queue.front() {
				eng.delivered(front)
			}

			for idx, step := range tt.steps {
				objects := map[string]Object{anchorName: anchor}
				sets := []controlplane.FlagSet{stay}

				if step.flags != "" {
					objects["tmpflags"] = Object{
						Kind:   KindFlags,
						Format: controlplane.FormatYAML,
						Bytes:  []byte(step.flags),
					}

					sets = append(
						sets,
						controlplane.FlagSet{
							Name:    tmpName,
							Sources: []string{"tmpflags"},
							APIKeys: []string{"key-tmp"},
						},
					)
				}

				objects[setsKey] = internalDoc(t, sets...)

				eng.round(context.Background(), &Content{Generation: uint64(idx + 2), Objects: objects}, nil)
				checkAnnounced(t, eng)

				for front := eng.queue.front(); step.deliver && front != nil; front = eng.queue.front() {
					eng.delivered(front)
				}

				checkAnnounced(t, eng)
			}

			known := []string{}

			for name := range eng.announced {
				if name != stayName {
					known = append(known, name)
				}
			}

			pending := make([]string, 0, len(eng.queue.byName))

			for name := range eng.queue.byName {
				pending = append(pending, name)
			}

			assert.ElementsMatch(t, tt.wantKnown, known)
			assert.ElementsMatch(t, tt.wantPending, pending)
		})
	}
}

// The number of events of a round is the number of flag sets for which the round leaves a change waiting.
func TestRoundResultCountsTheEventsLeftWaiting(t *testing.T) {
	t.Parallel()

	const (
		withFlag    = internalFlags
		withOther   = "y:\n  variations:\n    v: 2\n  defaultRule:\n    variation: v\n"
		withNothing = "{}\n"
	)

	type step struct {
		tmp        string // the flags file of the flag set "tmp"; empty means withdrawn
		other      string // the flags file of the flag set "other"; empty means withdrawn
		wantEvents int
		deliver    bool // the consumer reads everything that waits after this step
	}

	tests := []struct {
		name  string
		steps []step
	}{
		{
			name:  "two flag sets created in one round: one event each",
			steps: []step{{tmp: withFlag, other: withFlag, wantEvents: 2}},
		},
		{
			name: "a change merged into a waiting one counts once",
			steps: []step{
				{tmp: withFlag, wantEvents: 1},
				{tmp: withOther, wantEvents: 1},
			},
		},
		{
			name: "created and withdrawn before the consumer reads: it cancels out and counts zero",
			steps: []step{
				{tmp: withFlag, wantEvents: 1},
				{wantEvents: 0},
			},
		},
		{
			name: "a flag gained and lost before the consumer reads: it cancels out and counts zero",
			steps: []step{
				{tmp: withNothing, wantEvents: 0},
				{tmp: withFlag, wantEvents: 1},
				{tmp: withNothing, wantEvents: 0},
			},
		},
		{
			name: "a known flag set that is withdrawn: its removal counts once",
			steps: []step{
				{tmp: withFlag, deliver: true, wantEvents: 1},
				{wantEvents: 1},
			},
		},
		{
			name: "a round that changes nothing counts zero",
			steps: []step{
				{tmp: withFlag, deliver: true, wantEvents: 1},
				{tmp: withFlag, wantEvents: 0},
			},
		},
		{
			name: "one flag set cancels out while the other is merged",
			steps: []step{
				{tmp: withFlag, other: withFlag, wantEvents: 2},
				{other: withOther, wantEvents: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := flagdef.NewService()
			t.Cleanup(svc.Close)

			eng := NewEngine(nil, svc, NewHolder())

			for idx, step := range tt.steps {
				objects := map[string]Object{}
				sets := []controlplane.FlagSet{}

				for name, flags := range map[string]string{tmpName: step.tmp, "other": step.other} {
					if flags == "" {
						continue
					}

					objects[name+"flags"] = Object{
						Kind:   KindFlags,
						Format: controlplane.FormatYAML,
						Bytes:  []byte(flags),
					}
					sets = append(sets, controlplane.FlagSet{
						Name: name, Sources: []string{name + "flags"}, APIKeys: []string{"key-" + name},
					})
				}

				// the flag sets in a fixed order, whatever the order of the map
				slices.SortFunc(
					sets,
					func(left, right controlplane.FlagSet) int { return cmp.Compare(left.Name, right.Name) },
				)

				objects[setsKey] = internalDoc(t, sets...)

				eng.round(context.Background(), &Content{Generation: uint64(idx + 1), Objects: objects}, nil)
				checkAnnounced(t, eng)

				assert.Equal(t, step.wantEvents, eng.Result().Events, "events of step %d", idx)

				for front := eng.queue.front(); step.deliver && front != nil; front = eng.queue.front() {
					eng.delivered(front)
				}
			}
		})
	}
}
