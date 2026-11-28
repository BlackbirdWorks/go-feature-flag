package flagruntime_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

// TestEngine_AdminKeyCollision: a generation in which an API key of a flag set equals an admin key is rejected with
// the running one kept, and a bootstrap file whose admin key equals a running API key is rejected the same way.
func TestEngine_AdminKeyCollision(t *testing.T) {
	t.Parallel()

	const (
		secret = "ADMIN-SECRET-9"
		pathA  = "flagSets[a].apiKeys"
	)

	manyAdmin := "k-00, k-01, k-02, k-03, k-04, k-05, k-06, k-07, k-08, k-09, k-10, " + secret

	tests := []struct {
		name string
		// admin is the admin key of the first bootstrap file; reloadAdmin, when set, a second file.
		admin, reloadAdmin string
		wantCode           string
		wantDetail         string
		wantAdmin          string
		wantOutcome        flagruntime.Outcome
		// keys are the API keys of generation 1; laterKeys, when set, those of generation 2.
		keys, laterKeys []string
		// extraSets is the number of other flag sets s00, s01... whose key k-00, k-01... the caller lists as admin keys.
		extraSets      int
		wantGeneration uint64
	}{
		{
			name: "content", admin: secret, keys: []string{secret},
			wantOutcome: flagruntime.OutcomeRejected, wantCode: flagruntime.CodeAdminKeyCollision,
			wantDetail: pathA, wantAdmin: secret, wantGeneration: 0,
		},
		{
			name: "later generation", admin: secret, keys: []string{keyA}, laterKeys: []string{keyA, secret},
			wantOutcome: flagruntime.OutcomeRejected, wantCode: flagruntime.CodeAdminKeyCollision,
			wantDetail: pathA, wantAdmin: secret, wantGeneration: 1,
		},
		{
			name:           "bootstrap",
			admin:          adminOne,
			keys:           []string{secret},
			reloadAdmin:    secret,
			wantOutcome:    flagruntime.OutcomeRejected,
			wantCode:       flagruntime.CodeAdminKeyCollision,
			wantDetail:     "auth.adminKeys: equals an API key of flagSets[a].apiKeys",
			wantAdmin:      adminOne,
			wantGeneration: 1,
		},
		{
			name: "many admin keys", admin: "x1, x2, x3, " + secret, keys: []string{secret},
			wantOutcome: flagruntime.OutcomeRejected, wantCode: flagruntime.CodeAdminKeyCollision,
			wantDetail: pathA, wantAdmin: "x1", wantGeneration: 0,
		},
		{
			name: "names are capped", admin: manyAdmin, keys: []string{keyA}, extraSets: 11,
			wantOutcome: flagruntime.OutcomeRejected, wantCode: flagruntime.CodeAdminKeyCollision,
			wantDetail: "flagSets[s09].apiKeys, 1 more", wantAdmin: secret, wantGeneration: 0,
		},
		{
			name: "distinct keys", admin: secret, keys: []string{keyA},
			wantOutcome: flagruntime.OutcomeApplied, wantCode: flagruntime.CodeApplied,
			wantAdmin: secret, wantGeneration: 1,
		},
		{
			name: "fixed", admin: secret, keys: []string{secret}, laterKeys: []string{keyA},
			wantOutcome: flagruntime.OutcomeApplied, wantCode: flagruntime.CodeApplied,
			wantAdmin: secret, wantGeneration: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newRig(t, flagruntime.Hooks{})
			rig.start()

			rig.reload(bootstrapFromYAML(t, "version: 2\nauth:\n  adminKeys: ["+tt.admin+"]\n"))

			set := func(gen uint64, keys []string) flagruntime.RoundResult {
				sets := []controlplane.FlagSet{fset("a", src("f1"), keys...)}
				for i := range tt.extraSets {
					sets = append(sets, fset(fmt.Sprintf("s%02d", i), src("f1"), fmt.Sprintf("k-%02d", i)))
				}

				in := objs{"f1": flagsObj(1, "x"), setsName: setsObj(t, sets...)}

				return rig.feed(content(gen, in))
			}

			res := set(1, tt.keys)

			if tt.laterKeys != nil {
				res = set(2, tt.laterKeys)
			}

			if tt.reloadAdmin != "" {
				res = rig.reload(bootstrapFromYAML(t, "version: 2\nauth:\n  adminKeys: ["+tt.reloadAdmin+"]\n"))
			}

			current := rig.holder.Load()

			assert.Equal(t, tt.wantOutcome, res.Outcome, res.Detail)
			assert.Equal(t, tt.wantCode, res.Code)
			assert.Contains(t, res.Detail, tt.wantDetail)
			assert.NotContains(t, res.Detail, secret)
			assert.Equal(t, tt.wantGeneration, current.Generation)
			assert.True(t, isAdmin(current, tt.wantAdmin))
		})
	}
}
