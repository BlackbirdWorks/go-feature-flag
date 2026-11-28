package publish_test

import (
	"encoding/json"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/publish"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
)

const (
	pathOne    = "flags/one.yaml"
	pathTwo    = "flags/two.yaml"
	pathSets   = "flagsets/s.yaml"
	bucketName = "acme"
	chanStable = "stable"

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
				pathOne:            publicFlagsOne,
				pathTwo:            publicFlagsTwo,
				"flags/three.yaml": publicFlagsThree,
				pathSets:           setsHeader + tt.sets,
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

const (
	twoKeysFlags = "shared:\n  variations: {\"on\": true, \"off\": false}\n  defaultRule: {variation: \"off\"}\n" +
		"other:\n  variations: {\"on\": true, \"off\": false}\n  defaultRule: {variation: \"off\"}\n"
	twoKeylessSets = setsHeader + "  - name: one\n    sources: [one]\n  - name: two\n    sources: [two]\n"
)

func twoKeysTree() fstest.MapFS {
	return treeOf(map[string]string{
		pathOne: twoKeysFlags, pathTwo: twoKeysFlags, pathSets: twoKeylessSets,
	})
}

// With --output json the error lists every colliding flag key as one detail, in key order.
func TestPublishJSONErrorListsTheCollidingFlagKeys(t *testing.T) {
	t.Parallel()

	rig := newHarness(t, twoKeysTree())

	stdout, _, err := rig.run(t, withTarget(srcDir, "-o", jsonFlag)...)
	require.Error(t, err)
	assert.Equal(t, helper.ExitInvalid, helper.ExitCodeOf(err))

	var got struct {
		Error struct {
			Details []string `json:"details"`
		} `json:"error"`
	}

	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, []string{
		"flag other: defined in the keyless flag sets one (flag object one) and two (flag object two)",
		"flag shared: defined in the keyless flag sets one (flag object one) and two (flag object two)",
	}, got.Error.Details)
}

// A generation that an older tool published with a collision is reported by verify, one problem for each flag key
// (so that none is cut off), with the exit code of a bad state in the bucket (5).
func TestVerifyReportsEveryCollidingFlagKey(t *testing.T) {
	t.Parallel()

	rig := newHarness(t, fstest.MapFS{
		pathOne: {Data: []byte(twoKeysFlags)}, pathTwo: {Data: []byte(twoKeysFlags)},
		pathSets: {Data: []byte(setsHeader + "  - name: one\n    sources: [one]\n    apiKeys: [k]\n")},
	})
	rig.publishOnce(t)
	require.NotEmpty(t, rig.names)

	// the same tree with a flag set without keys, published without the checks of the CLI
	var tree fs.FS = twoKeysTree()

	objects, err := publish.LoadSource(tree, srcDir, publish.SourceLimits{})
	require.NoError(t, err)

	pub, err := s3publish.New(rig.fake, &s3publish.Config{
		Bucket: bucketName, Channel: chanStable, Connection: rig.names[0], ManifestKey: manifestKey, Concurrency: 1,
	})
	require.NoError(t, err)

	_, err = pub.Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	stdout, _, err := rig.run(t, withTarget(verifyWord)...)
	require.Error(t, err)
	assert.Equal(t, helper.ExitVerify, helper.ExitCodeOf(err))
	assert.Contains(t, err.Error(), "verification failed: 2 problems")
	assert.Contains(t, stdout, "flag other: defined in the keyless flag sets one")
	assert.Contains(t, stdout, "flag shared: defined in the keyless flag sets one")
}
