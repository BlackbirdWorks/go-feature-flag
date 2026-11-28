package flagruntime_test

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

// TestEngine_AdminKeyCollision: an API key of a flag set that is also an admin key is not routed to the flag set, the
// flag set reports the problem and serves its other keys, the generation is applied, and a reload of the bootstrap
// file derives the routing again from the content that runs.
func TestEngine_AdminKeyCollision(t *testing.T) {
	t.Parallel()

	const (
		secret  = "ADMIN-SECRET-9"
		secret2 = "ADMIN-SECRET-10"
	)

	tests := []struct {
		name string
		// admin is the admin key of the first bootstrap file; reloadAdmin, when set, a second file.
		admin, reloadAdmin string
		wantState          flagruntime.SetState
		// keys are the API keys of generation 1, keysB those of a second flag set b; laterKeys, when set, are the
		// keys of a in generation 2, again publishes generation 2 with the same keys.
		keys, keysB, laterKeys []string
		wantWarnings           int
		wantAllDisabled        int
		wantAPIKeys            int
		wantAdmins             int
		wantDisabled           int
		wantProblemsB          int
		wantGeneration         uint64
		again                  bool
		wantProblem            bool
		wantSecretAdm          bool
		wantSecretSet          bool
		wantOtherSet           bool
	}{
		{
			name:            "content",
			admin:           secret,
			keys:            []string{secret, keyA},
			wantState:       flagruntime.StateDegraded,
			wantWarnings:    1,
			wantAllDisabled: 1,
			wantAPIKeys:     1,
			wantDisabled:    1,
			wantGeneration:  1,
			wantProblem:     true,
			wantOtherSet:    true,
		},
		{
			name:            "logged once",
			admin:           secret,
			keys:            []string{secret, keyA},
			again:           true,
			wantState:       flagruntime.StateDegraded,
			wantWarnings:    1,
			wantAllDisabled: 1,
			wantAPIKeys:     1,
			wantDisabled:    1,
			wantGeneration:  2,
			wantProblem:     true,
			wantOtherSet:    true,
		},
		{
			name:            "rotated away",
			admin:           secret,
			reloadAdmin:     "rotated-admin",
			keys:            []string{secret, keyA},
			wantState:       flagruntime.StateServing,
			wantWarnings:    1,
			wantAllDisabled: 1,
			wantAPIKeys:     2,
			wantAdmins:      1,
			wantGeneration:  1,
			wantSecretSet:   true,
			wantOtherSet:    true,
		},
		{
			name:            "key leaves the flag set",
			admin:           secret,
			keys:            []string{secret, keyA},
			laterKeys:       []string{keyA},
			wantState:       flagruntime.StateServing,
			wantWarnings:    1,
			wantAllDisabled: 1,
			wantAPIKeys:     1,
			wantAdmins:      1,
			wantGeneration:  2,
			wantSecretAdm:   true,
			wantOtherSet:    true,
		},
		{
			name:            "introduced by reload",
			admin:           adminOne,
			reloadAdmin:     secret,
			keys:            []string{secret, keyA},
			wantState:       flagruntime.StateDegraded,
			wantWarnings:    1,
			wantAllDisabled: 1,
			wantAPIKeys:     1,
			wantDisabled:    1,
			wantGeneration:  1,
			wantProblem:     true,
			wantOtherSet:    true,
		},
		{
			name: "only key", admin: secret, keys: []string{secret}, wantState: flagruntime.StateDegraded,
			wantWarnings: 1, wantAllDisabled: 1, wantDisabled: 1, wantGeneration: 1, wantProblem: true,
		},
		{
			name: "other admin keys work", admin: "x1, x2, x3, " + secret, keys: []string{secret},
			wantState: flagruntime.StateDegraded, wantWarnings: 1, wantAdmins: 3, wantDisabled: 1, wantGeneration: 1,
			wantProblem: true,
		},
		{
			name:            "prior problem is kept",
			admin:           secret,
			keys:            []string{keyA},
			keysB:           []string{keyA, secret},
			wantState:       flagruntime.StateServing,
			wantWarnings:    1,
			wantAllDisabled: 1,
			wantAPIKeys:     1,
			wantDisabled:    1,
			wantGeneration:  1,
			wantProblemsB:   2,
			wantOtherSet:    true,
		},
		{
			name:            "second key same set",
			admin:           secret + ", " + secret2,
			keys:            []string{secret},
			laterKeys:       []string{secret, secret2},
			wantState:       flagruntime.StateDegraded,
			wantWarnings:    2,
			wantAllDisabled: 1,
			wantDisabled:    2,
			wantGeneration:  2,
			wantProblem:     true,
		},
		{
			name:           "one of two keys",
			admin:          secret + ", " + secret2,
			keys:           []string{secret},
			wantState:      flagruntime.StateDegraded,
			wantWarnings:   1,
			wantAdmins:     1,
			wantDisabled:   1,
			wantGeneration: 1,
			wantProblem:    true,
		},
		{
			name: "distinct keys", admin: secret, keys: []string{keyA}, wantState: flagruntime.StateServing,
			wantAPIKeys: 1, wantAdmins: 1, wantGeneration: 1, wantSecretAdm: true, wantOtherSet: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := newRecordingHandler()
			rig := newRigLogger(t, slog.New(handler), flagruntime.Hooks{})
			rig.start()

			rig.reload(bootstrapFromYAML(t, "version: 2\nauth:\n  adminKeys: ["+tt.admin+"]\n"))

			feed := func(gen uint64) flagruntime.RoundResult {
				keys := tt.keys
				if gen == 2 && tt.laterKeys != nil {
					keys = tt.laterKeys
				}

				sets := []controlplane.FlagSet{fset("a", src("f1"), keys...)}
				if tt.keysB != nil {
					sets = append(sets, fset("b", src("f1"), tt.keysB...))
				}

				in := objs{"f1": flagsObj(int(gen), "x"), setsName: setsObj(t, sets...)}

				return rig.feed(content(gen, in))
			}

			res := feed(1)
			if tt.again || tt.laterKeys != nil {
				res = feed(2)
			}

			if tt.reloadAdmin != "" {
				res = rig.reload(bootstrapFromYAML(t, "version: 2\nauth:\n  adminKeys: ["+tt.reloadAdmin+"]\n"))
			}

			current := rig.holder.Load()
			status := current.Status.FlagSets["a"]
			require.NotNil(t, status)

			problem := false

			for _, p := range status.Problems {
				problem = problem || p.Code == flagruntime.CodeAdminKeyCollision
			}

			_, secretSet := routedSet(current, secret)
			_, otherSet := routedSet(current, keyA)

			assert.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)
			assert.Equal(t, tt.wantGeneration, current.Generation)
			assert.Equal(t, tt.wantState, status.State)
			assert.Equal(t, tt.wantAPIKeys, status.APIKeys)
			assert.Equal(t, tt.wantProblem, problem)
			assert.Equal(t, tt.wantSecretAdm, isAdmin(current, secret))
			assert.Equal(t, tt.wantSecretSet, secretSet)
			assert.Equal(t, tt.wantOtherSet, otherSet)
			assert.Equal(t, tt.wantWarnings, strings.Count(handler.text(), "an admin key is disabled"))
			assert.Equal(t, tt.wantAllDisabled, strings.Count(handler.text(), "every admin key is disabled"))
			assert.Len(t, current.AdminKeys, tt.wantAdmins)
			assert.Equal(t, tt.wantDisabled, current.Status.DisabledAdminKeys)
			assert.NotContains(t, handler.text(), secret)

			if tt.keysB != nil {
				assert.Len(t, current.Status.FlagSets["b"].Problems, tt.wantProblemsB)
			}
		})
	}
}

// TestEngine_ContentDecision: the decision of a revision is about its content, so a bootstrap change that is applied
// in the same round does not make a rejected content applied.
func TestEngine_ContentDecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		wantOutcome  flagruntime.Outcome
		wantCode     string
		wantRoundOut flagruntime.Outcome
		generation   uint64
		wantRunning  uint64
	}{
		{
			name: "invalid content", generation: 0, wantOutcome: flagruntime.OutcomeRejected,
			wantCode: flagruntime.CodeInvalidContent, wantRunning: 0, wantRoundOut: flagruntime.OutcomeApplied,
		},
		{
			name: "valid content", generation: 1, wantOutcome: flagruntime.OutcomeApplied,
			wantCode: flagruntime.CodeApplied, wantRunning: 1, wantRoundOut: flagruntime.OutcomeApplied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newRig(t, flagruntime.Hooks{})

			c := content(tt.generation, simpleObjs(t, 1))
			c.Revision = 7

			// both inputs wait for the engine, so they are one round
			rig.eng.Submit(c)
			rig.eng.Reload(bootstrapFromYAML(t, "version: 2\nauth:\n  adminKeys: [rotated]\n"))
			rig.start()

			round := rig.nextRound()

			decision, err := rig.eng.AwaitDecision(t.Context(), 7)
			require.NoError(t, err)

			assert.Equal(t, tt.wantOutcome, decision.Reload.Outcome)
			assert.Equal(t, tt.wantCode, decision.Reload.Code)
			assert.Equal(t, tt.wantRunning, decision.Running)
			assert.Equal(t, tt.wantRoundOut, round.Outcome, "the round itself applied the bootstrap change")
		})
	}
}
