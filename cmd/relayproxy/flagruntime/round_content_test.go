package flagruntime_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

// A round reports whether it stored the content it took. A round that stores a bootstrap change (an admin key) and
// rejects the content of its revision is an applied round, but not a content decision: whoever saves the applied
// content must not save that revision.
func TestRoundResultSaysWhetherTheContentWasStored(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	entered := make(chan struct{}, 1)

	harness := newRig(t, flagruntime.Hooks{AfterRound: func(res flagruntime.RoundResult) {
		if res.Revision == 1 {
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

	harness.eng.Submit(revision(content(1, simpleObjs(t, 1)), 1))

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the engine did not end its first round")
	}

	// both wait while the engine is held: the next round takes the rejected content and the bootstrap file together
	bad := revision(content(2, simpleObjs(t, 2).with(objs{"f1": {Kind: flagruntime.KindFlags, State: objectBogus}})), 2)
	harness.eng.Submit(bad)
	harness.eng.Reload(bootstrapFromYAML(t, "version: 2\nauth:\n  adminKeys: [CANARY-ADMIN-KEY-2]\n"))

	close(gate)

	first := harness.nextRound()
	assert.Equal(t, uint64(1), first.Revision)
	assert.True(t, first.ContentApplied, "the first round stored its content")

	second := harness.nextRound()
	assert.Equal(t, uint64(2), second.Revision)
	assert.Equal(t, flagruntime.OutcomeApplied, second.Outcome, "the bootstrap change was stored")
	assert.False(t, second.ContentApplied, "the content of the revision was not")
	assert.Equal(t, uint64(1), harness.holder.Load().Generation, "the runtime still runs the first generation")
}
