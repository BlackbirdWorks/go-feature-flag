package publish_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

const (
	publicFlagsOne = "shared:\n  variations: {\"on\": true, \"off\": false}\n  defaultRule: {variation: \"off\"}\n"
	publicFlagsTwo = "shared:\n  variations: {\"on\": true, \"off\": false}\n  defaultRule: {variation: \"on\"}\n" +
		"other:\n  variations: {\"on\": true, \"off\": false}\n  defaultRule: {variation: \"off\"}\n"
	publicFlagsThree = "third:\n  variations: {\"on\": true, \"off\": false}\n  defaultRule: {variation: \"off\"}\n"
	setsHeader       = "apiVersion: semaphore/v1\nkind: FlagSet\nflagSets:\n"
)

// The flag sets without API keys are one public namespace: a publication in which two of them define the same flag
// key is refused before any call, because the relay would reject the generation. The same flag key in a flag set with
// keys, or in one keyless set only, is fine.
func TestPublishRefusesAFlagKeySharedByKeylessFlagSets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sets      string
		wantError string
		wantCalls bool
	}{
		{
			name:      "two keyless sets share a key",
			sets:      "  - name: one\n    sources: [one]\n  - name: two\n    sources: [two]\n",
			wantError: "flag shared: defined in the keyless flag sets one (flag object one) and two (flag object two)",
		},
		{
			name: "a keyed set may share it",
			sets: "  - name: one\n    sources: [one]\n  - name: two\n    sources: [two]\n" +
				"    apiKeys: [" + canaryAPIKey + "]\n",
			wantCalls: true,
		},
		{
			name:      "keyless sets with other keys",
			sets:      "  - name: one\n    sources: [one]\n  - name: three\n    sources: [three]\n",
			wantCalls: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newHarness(t, treeOf(map[string]string{
				"flags/one.yaml":   publicFlagsOne,
				"flags/two.yaml":   publicFlagsTwo,
				"flags/three.yaml": publicFlagsThree,
				"flagsets/s.yaml":  setsHeader + tt.sets,
			}))

			_, _, err := rig.run(t, withTarget(srcDir)...)

			if tt.wantError != "" {
				require.Error(t, err)
				assert.Equal(t, helper.ExitInvalid, helper.ExitCodeOf(err))
				assert.Contains(t, err.Error(), tt.wantError)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantCalls, len(rig.fake.Calls()) > 0, "nothing is read or written when it is refused")
		})
	}
}
