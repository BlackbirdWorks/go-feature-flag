package relaycore

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

func TestReloadCounter(t *testing.T) {
	t.Parallel()

	round := func(outcome flagruntime.Outcome, code string, gen uint64) flagruntime.RoundResult {
		var res flagruntime.RoundResult

		res.Outcome, res.Code, res.Generation = outcome, code, gen

		return res
	}
	summary := func(outcome, code string, gen uint64) *RoundSummary {
		return &RoundSummary{At: clock.Now().UTC(), Outcome: outcome, Code: code, Generation: gen}
	}

	applied := flagruntime.OutcomeApplied
	rejected := flagruntime.OutcomeRejected
	ignored := flagruntime.OutcomeIgnored

	tests := []struct {
		name   string
		rounds []flagruntime.RoundResult
		want   ReloadStats
	}{
		{name: "none", want: ReloadStats{}},
		{
			name: "rejected then ignored keeps the rejection",
			rounds: []flagruntime.RoundResult{
				round(applied, "applied", 1), round(rejected, "invalid-content", 2), round(ignored, "unchanged", 1),
			},
			want: ReloadStats{
				Applied: 1, Rejected: 1, Ignored: 1, LastSuccess: clock.Now().UTC(),
				LastRound:    summary("ignored", "unchanged", 1),
				LastApplied:  summary("applied", "applied", 1),
				LastRejected: summary("rejected", "invalid-content", 2),
			},
		},
		{
			name: "rejected then applied clears the rejection",
			rounds: []flagruntime.RoundResult{
				round(rejected, "vetoed", 2), round(applied, "applied", 3),
			},
			want: ReloadStats{
				Applied: 1, Rejected: 1, LastSuccess: clock.Now().UTC(),
				LastRound:   summary("applied", "applied", 3),
				LastApplied: summary("applied", "applied", 3),
			},
		},
		{
			name:   "only rejected leaves no success",
			rounds: []flagruntime.RoundResult{round(rejected, "vetoed", 2)},
			want: ReloadStats{
				Rejected:     1,
				LastRound:    summary("rejected", "vetoed", 2),
				LastRejected: summary("rejected", "vetoed", 2),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var counter reloadCounter
			for _, r := range tt.rounds {
				counter.record(r)
			}

			assert.Equal(t, tt.want, counter.snapshot())
		})
	}
}
