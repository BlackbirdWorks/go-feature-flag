//go:build docker

package valkeysegment_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

// TestE2EAtomicExistence runs the atomic check and the plain pair on Valkey and on Redis and counts the commands that
// the server served for one lookup call.
func TestE2EAtomicExistence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		image      string
		wantStates []segment.State
		wantMulti  int
		disable    bool
	}{
		{
			name: "valkey atomic", image: valkeyImage, wantMulti: 2,
			wantStates: []segment.State{segment.Member, segment.NotMember, segment.Unknown},
		},
		{
			name: "valkey off", image: valkeyImage, disable: true,
			wantStates: []segment.State{segment.Member, segment.NotMember, segment.Unknown},
		},
		{
			name: "redis atomic", image: redisImage, wantMulti: 2,
			wantStates: []segment.State{segment.Member, segment.NotMember, segment.Unknown},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := startServerImage(t, tt.image)
			opt := srv.option(plainDial)
			client := seed(t, opt)

			load(t, client, keyBeta, "id1")

			prov := e2eProvider(t, opt, &valkeysegment.Options{
				MissingKey:    valkeysegment.MissingUnknown,
				Timeout:       e2eTimeout,
				DisableAtomic: tt.disable,
				Segments: map[string]valkeysegment.SegmentOptions{
					segBeta:  {Key: keyBeta},
					segGhost: {Key: "seg:{ghost}"},
				},
			})
			require.NoError(t, prov.Init(context.Background()))

			before := map[string]int{}
			for _, name := range []string{"multi", "exec", "exists", "smismember"} {
				before[name] = calls(t, client, name)
			}

			lookups := []segment.Lookup{lk(segBeta, "id1"), lk(segBeta, "zed"), lk(segGhost, "id1")}

			require.NoError(t, prov.Contains(context.Background(), lookups))
			assert.Equal(t, tt.wantStates, states(lookups))
			assert.Equal(t, tt.wantMulti, calls(t, client, "multi")-before["multi"], "one MULTI for each set")
			assert.Equal(t, tt.wantMulti, calls(t, client, "exec")-before["exec"], "EXEC")
			assert.Equal(t, 2, calls(t, client, "exists")-before["exists"], "one EXISTS for each set")
			assert.Equal(t, 2, calls(t, client, "smismember")-before["smismember"], "one SMISMEMBER for each set")
		})
	}
}
