package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

func TestEngineEqualGenerationRule(t *testing.T) {
	t.Parallel()

	base := simpleObjs(t, 1)
	stale := func(reason string) flagruntime.Object {
		return flagruntime.Object{Kind: flagruntime.KindFlags, State: flagruntime.ObjectStale, Reason: reason}
	}
	withF1 := func(object flagruntime.Object) objs { return base.with(objs{"f1": object}) }

	tests := []struct {
		first          objs
		second         objs
		name           string
		wantOutcome    flagruntime.Outcome
		wantCode       string
		wantSetState   flagruntime.SetState
		wantSnapshot   uint64
		wantRunsSecond bool
	}{
		{
			name: "the same content again is ignored", first: base, second: base,
			wantOutcome: flagruntime.OutcomeIgnored, wantCode: flagruntime.CodeDuplicate,
			wantSetState: flagruntime.StateServing, wantSnapshot: 1,
		},
		{
			name:           "pending to ok is built",
			first:          withF1(flagruntime.Object{Kind: flagruntime.KindFlags, State: flagruntime.ObjectPending}),
			second:         base,
			wantOutcome:    flagruntime.OutcomeApplied,
			wantCode:       flagruntime.CodeApplied,
			wantSetState:   flagruntime.StateServing,
			wantSnapshot:   2,
			wantRunsSecond: true,
		},
		{
			name:           "missing to ok is built",
			first:          withF1(flagruntime.Object{Kind: flagruntime.KindFlags, State: flagruntime.ObjectMissing}),
			second:         base,
			wantOutcome:    flagruntime.OutcomeApplied,
			wantCode:       flagruntime.CodeApplied,
			wantSetState:   flagruntime.StateServing,
			wantSnapshot:   2,
			wantRunsSecond: true,
		},
		{
			name: "stale to ok is built", first: withF1(stale("etag")), second: base,
			wantOutcome: flagruntime.OutcomeApplied, wantCode: flagruntime.CodeApplied,
			wantSetState: flagruntime.StateServing, wantSnapshot: 2, wantRunsSecond: true,
		},
		{
			name: "ok to stale is built and keeps the last good flags", first: base, second: withF1(stale("etag")),
			wantOutcome: flagruntime.OutcomeApplied, wantCode: flagruntime.CodeApplied,
			wantSetState: flagruntime.StateDegraded, wantSnapshot: 2, wantRunsSecond: true,
		},
		{
			name: "ok to missing is built", first: base,
			second:      withF1(flagruntime.Object{Kind: flagruntime.KindFlags, State: flagruntime.ObjectMissing}),
			wantOutcome: flagruntime.OutcomeApplied, wantCode: flagruntime.CodeApplied,
			wantSetState: flagruntime.StateDegraded, wantSnapshot: 2, wantRunsSecond: true,
		},
		{
			name: "ok to pending is built", first: base,
			second:      withF1(flagruntime.Object{Kind: flagruntime.KindFlags, State: flagruntime.ObjectPending}),
			wantOutcome: flagruntime.OutcomeApplied, wantCode: flagruntime.CodeApplied,
			wantSetState: flagruntime.StateDegraded, wantSnapshot: 2, wantRunsSecond: true,
		},
		{
			name: "a new reason of the same state is built", first: withF1(stale("one")), second: withF1(stale("two")),
			wantOutcome: flagruntime.OutcomeApplied, wantCode: flagruntime.CodeApplied,
			wantSetState: flagruntime.StatePending, wantSnapshot: 2, wantRunsSecond: true,
		},
		{
			name:  "stale to ok with other bytes is built: nothing carried content on the stale side",
			first: withF1(stale("etag")), second: withF1(flagsObj(2, "x", "y")),
			wantOutcome: flagruntime.OutcomeApplied, wantCode: flagruntime.CodeApplied,
			wantSetState: flagruntime.StateServing, wantSnapshot: 2, wantRunsSecond: true,
		},
		{
			name:  "another checksum for a name where both sides carry content is a conflict",
			first: base, second: withF1(flagsObj(2, "x", "y")),
			wantOutcome: flagruntime.OutcomeRejected, wantCode: flagruntime.CodeGenerationConflict,
			wantSetState: flagruntime.StateServing, wantSnapshot: 1,
		},
		{
			name:         "an additional object is a conflict",
			first:        base,
			second:       base.with(objs{extraName: flagsObj(1, "z")}),
			wantOutcome:  flagruntime.OutcomeRejected,
			wantCode:     flagruntime.CodeGenerationConflict,
			wantSetState: flagruntime.StateServing,
			wantSnapshot: 1,
		},
		{
			name: "a missing object is a conflict", first: base.with(objs{extraName: flagsObj(1, "z")}), second: base,
			wantOutcome: flagruntime.OutcomeRejected, wantCode: flagruntime.CodeGenerationConflict,
			wantSetState: flagruntime.StateServing, wantSnapshot: 1,
		},
		{
			name: "another object of the same count is a conflict", first: base,
			second:      base.without("f1").with(objs{"f9": flagsObj(1, "x", "y")}),
			wantOutcome: flagruntime.OutcomeRejected, wantCode: flagruntime.CodeGenerationConflict,
			wantSetState: flagruntime.StateServing, wantSnapshot: 1,
		},
		{
			name: "another kind for a name is a conflict", first: base,
			second:      withF1(obj(flagruntime.KindFlagSets, flagsYAML(1, "x"))),
			wantOutcome: flagruntime.OutcomeRejected, wantCode: flagruntime.CodeGenerationConflict,
			wantSetState: flagruntime.StateServing, wantSnapshot: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			harness := newRig(t, flagruntime.Hooks{})
			harness.start()

			harness.feed(content(5, tt.first))

			before := harness.holder.Load()
			res := harness.feed(content(5, tt.second))
			after := harness.holder.Load()

			assert.Equal(t, tt.wantOutcome, res.Outcome)
			assert.Equal(t, tt.wantCode, res.Code)
			assert.Equal(t, tt.wantSnapshot, after.Snapshot)
			assert.Equal(t, uint64(5), after.Generation)

			if tt.wantRunsSecond {
				assert.NotSame(t, before, after)
			} else {
				assert.Same(t, before, after, "nothing is stored")
			}

			set, ok := after.FlagSetByKey(keyA)
			require.True(t, ok)
			assert.Equal(t, tt.wantSetState, set.State)
		})
	}
}
