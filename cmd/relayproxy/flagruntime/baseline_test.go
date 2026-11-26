package flagruntime_test

import (
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

	assert.Equal(t, flagruntime.Reload{}, harness.eng.Result())
}
