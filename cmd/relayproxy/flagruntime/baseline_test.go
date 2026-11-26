package flagruntime_test

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

// A first configuration that changes nothing must still be the baseline of pendingRestart.
func TestEngineFirstUnchangedBootstrapIsTheBaseline(t *testing.T) {
	t.Parallel()

	const first = "version: 2\nserver:\n  listeners:\n    - {name: api, address: \":8080\"}\n"

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	before := harness.holder.Load()
	res := harness.reload(bootstrapFromYAML(t, first))

	assert.Equal(t, flagruntime.OutcomeIgnored, res.Outcome, "the first configuration changes nothing")
	assert.Same(t, before, harness.holder.Load())

	res = harness.reload(
		bootstrapFromYAML(t, "version: 2\nserver:\n  listeners:\n    - {name: api, address: \":9090\"}\n"),
	)

	assert.Equal(t, flagruntime.OutcomeApplied, res.Outcome)
	assert.Equal(t, []string{flagruntime.SettingServerListeners}, harness.holder.Load().Status.PendingRestart)
}

func TestEngineResultBeforeAnyRound(t *testing.T) {
	t.Parallel()

	harness := newRig(t, flagruntime.Hooks{})

	assert.Equal(t, flagruntime.RoundResult{}, harness.eng.Result())
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
