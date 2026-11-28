package flagruntime_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

func revision(c *flagruntime.Content, n uint64) *flagruntime.Content {
	c.Revision = n

	return c
}

// await runs Await in a goroutine and returns the channel of its answer.
func await(t *testing.T, eng *flagruntime.Engine, holder *flagruntime.Holder, rev uint64) chan flagruntime.Reload {
	t.Helper()

	out := make(chan flagruntime.Reload, 1)

	go func() {
		got, err := eng.Await(t.Context(), rev)
		assert.NoError(t, err)

		// what the call answers is what the runtime serves: the recovered flags are there when it returns
		flagSet, ok := holder.Load().FlagSets["a"]
		assert.True(t, ok && flagSet.Flags != nil)

		if ok {
			assert.Contains(t, flagSet.Flags, "x", "the flags served when the call returned")
		}

		out <- got
	}()

	return out
}

// Await waits for the revision of a snapshot, not for a generation: an object that recovers at the generation the
// runtime already serves is a revision of its own, decided only when the engine applied it, and the engine that
// keeps the latest content only answers the waiters of the revisions it dropped.
func TestEngineAwaitsTheRevision(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	entered := make(chan struct{}, 8)

	harness := newRig(t, flagruntime.Hooks{BeforeSwap: func(candidate *flagruntime.Runtime) error {
		if candidate.Snapshot >= 2 {
			entered <- struct{}{}

			<-gate
		}

		return nil
	}})
	harness.start()

	// registered after the rig: the cleanups run last in, first out, so the gate opens before the rig stops the
	// engine, and a run that failed ends at once
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})

	stale := flagruntime.Object{Kind: flagruntime.KindFlags, State: flagruntime.ObjectStale, Reason: "gone"}
	res := harness.feed(revision(content(1, simpleObjs(t, 1).with(objs{"f1": stale})), 1))
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome)

	applied, err := harness.eng.Await(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, flagruntime.OutcomeApplied, applied.Outcome)

	// the object recovers at the same generation: the runtime serves generation 1 all along, the revision 2 is not
	// decided before the engine applied it
	harness.eng.Submit(revision(content(1, simpleObjs(t, 1)), 2))

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the engine did not reach the swap")
	}

	// a call that is cancelled while the engine is paused is not answered with a decision: there is none yet
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = harness.eng.Await(cancelled, 2)
	require.ErrorIs(t, err, context.Canceled, "the recovery is not decided before the engine applied it")

	waited := await(t, harness.eng, harness.holder, 2)

	assert.Empty(t, harness.holder.Load().FlagSets["a"].Flags, "the flag set serves no flag before the apply")

	// the revisions 3 and 4 wait while the engine is paused: it keeps the latest, and both waiters are answered
	harness.eng.Submit(revision(content(2, simpleObjs(t, 2)), 3))
	harness.eng.Submit(revision(content(3, simpleObjs(t, 3)), 4))

	third, fourth := await(t, harness.eng, harness.holder, 3), await(t, harness.eng, harness.holder, 4)

	close(gate)

	for name, ch := range map[string]chan flagruntime.Reload{"2": waited, "3": third, "4": fourth} {
		select {
		case got := <-ch:
			assert.Equal(t, flagruntime.OutcomeApplied, got.Outcome, "revision "+name)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "a call did not return", "revision %s", name)
		}
	}

	assert.Contains(t, harness.holder.Load().FlagSets["a"].Flags, "x", "the flag set serves the recovered flags")
}

// A content the engine rejects is a decision too, with its code; a cancelled context ends the wait.
func TestEngineAwaitRejectedAndCancelled(t *testing.T) {
	t.Parallel()

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	res := harness.feed(revision(content(1, simpleObjs(t, 1)), 1))
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome)

	bad := revision(content(2, simpleObjs(t, 2).with(objs{"f1": {Kind: flagruntime.KindFlags, State: "bogus"}})), 2)
	res = harness.feed(bad)
	require.Equal(t, flagruntime.OutcomeRejected, res.Outcome)

	rejected, err := harness.eng.Await(t.Context(), 2)
	require.NoError(t, err)
	assert.Equal(t, flagruntime.OutcomeRejected, rejected.Outcome)
	assert.Equal(t, flagruntime.CodeInvalidContent, rejected.Code)

	none, err := harness.eng.Await(t.Context(), 0)
	require.NoError(t, err, "revision 0 has nothing to wait for")
	assert.Equal(t, flagruntime.OutcomeApplied, none.Outcome)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = harness.eng.Await(ctx, 9)
	require.ErrorIs(t, err, context.Canceled)
}

// The decision of a revision comes with the generation the engine served when it decided: a swap that is stored after
// the decision, and before the caller looks at the runtime, does not change the answer.
func TestEngineAwaitDecisionHoldsTheGenerationOfTheDecision(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	entered := make(chan struct{}, 8)

	harness := newRig(t, flagruntime.Hooks{AfterSwap: func(candidate *flagruntime.Runtime) {
		if candidate.Generation >= 2 {
			entered <- struct{}{}

			<-gate
		}
	}})
	harness.start()

	// registered after the rig: the gate opens before the rig stops the engine
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})

	res := harness.feed(revision(content(1, simpleObjs(t, 1)), 1))
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome)

	// the generation 2 is stored, and the engine is held before it ends the round: the decision of the revision 1 is
	// the last one
	harness.eng.Submit(revision(content(2, simpleObjs(t, 2)), 2))

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the engine did not reach the swap")
	}

	require.Equal(t, uint64(2), harness.holder.Load().Generation, "the runtime already serves the generation 2")

	first, err := harness.eng.AwaitDecision(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, flagruntime.OutcomeApplied, first.Reload.Outcome)
	assert.Equal(t, uint64(1), first.Reload.Generation)
	assert.Equal(t, uint64(1), first.Running, "the generation of the decision, not the one stored since")

	close(gate)

	second, err := harness.eng.AwaitDecision(t.Context(), 2)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), second.Running)
}
