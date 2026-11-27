package relaycore

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

func TestReloadCounter(t *testing.T) {
	t.Parallel()

	round := func(outcome flagruntime.Outcome) flagruntime.RoundResult {
		var res flagruntime.RoundResult

		res.Outcome = outcome

		return res
	}

	tests := []struct {
		name   string
		rounds []flagruntime.RoundResult
		want   ReloadStats
	}{
		{name: "none", want: ReloadStats{}},
		{
			name: "mixed",
			rounds: []flagruntime.RoundResult{
				round(flagruntime.OutcomeApplied), round(flagruntime.OutcomeRejected),
				round(flagruntime.OutcomeIgnored), round(flagruntime.OutcomeIgnored), round(flagruntime.OutcomeApplied),
			},
			want: ReloadStats{Applied: 2, Rejected: 1, Ignored: 2, LastSuccess: clock.Now().UTC()},
		},
		{
			name:   "only rejected leaves no success time",
			rounds: []flagruntime.RoundResult{round(flagruntime.OutcomeRejected)},
			want:   ReloadStats{Rejected: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var counter reloadCounter
			for _, r := range tt.rounds {
				counter.record(r)
			}

			assert.Equal(t, tt.want, counter.stats())
		})
	}
}
