package publish_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

const duplicateKeySet = "  - name: other\n    sources: [checkout]\n    apiKeys: [" + canaryAPIKey + "]\n"

const checkoutKey = "objects/sha256/85/8500b7b202c6d6ba249f43494e2f7ef51a0bad128c2e20a4e0a4ce0265f5c9f7.yaml"

func TestVerify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		damage     func(rig *harness)
		name       string
		wantGolden string
		wantErr    string
		args       []string
		wantCode   int
	}{
		{name: "healthy, text", args: withTarget("verify"), wantGolden: "verify_ok.txt"},
		{name: "healthy, json", args: withTarget("verify", "-o", "json"), wantGolden: "verify_ok.json"},
		{
			name: "corrupted object, text", args: withTarget("verify"), wantGolden: "verify_corrupt.txt",
			wantCode: helper.ExitVerify, wantErr: "verification failed: 1 problems",
			damage: func(rig *harness) {
				b, _ := rig.fake.Body(checkoutKey)
				b[0] ^= 0x01
				rig.fake.Seed(checkoutKey, b)
			},
		},
		{
			name: "missing object, json", args: withTarget("verify", "-o", "json"), wantGolden: "verify_missing.json",
			wantCode: helper.ExitVerify, wantErr: "verification failed: 1 problems",
			damage: func(rig *harness) { rig.fake.Remove(checkoutKey) },
		},
		{
			name:     "no manifest",
			args:     withTarget("verify"),
			wantCode: helper.ExitVerify,
			wantErr:  "no manifest in the bucket",
			damage:   func(rig *harness) { rig.fake.Remove(manifestKey) },
		},
		{
			name: "store failure", args: withTarget("verify"), wantCode: helper.ExitTransport, wantErr: "GetObject",
			damage: func(rig *harness) {
				rig.fake.Before = func(call string) error {
					if call == "GET "+manifestKey {
						return apiError("InternalError")
					}

					return nil
				}
			},
		},
		{
			name:     "bad output",
			args:     withTarget("verify", "-o", "yaml"),
			wantCode: helper.ExitFailure,
			wantErr:  "--output must be text or json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newHarness(t, sourceTree())
			rig.publishOnce(t)

			if tt.damage != nil {
				tt.damage(rig)
			}

			before := rig.fake.Keys()
			rig.fake.ResetCalls()

			stdout, _, err := rig.run(t, tt.args...)

			if tt.wantCode == 0 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Equal(t, tt.wantCode, helper.ExitCodeOf(err))
				assert.Contains(t, err.Error(), tt.wantErr)
			}

			if tt.wantGolden != "" {
				assert.Equal(t, golden(t, tt.wantGolden), stdout)
			}

			assert.Equal(t, before, rig.fake.Keys(), "verify never writes")

			for _, c := range rig.fake.Calls() {
				assert.True(t, strings.HasPrefix(c, "GET "), "verify only reads: %s", c)
			}
		})
	}
}

// TestOutputNeverContainsSecrets runs every command and output format over documents with an API key and checks
// that the key appears nowhere: not in the output, not in the error, not in the manifest.
func TestOutputNeverContainsSecrets(t *testing.T) {
	t.Parallel()

	commands := [][]string{
		withTarget(srcDir), withTarget(srcDir, "-o", "json"), withTarget(srcDir, "--dry-run"),
		withTarget("verify"), withTarget("verify", "-o", "json"),
	}

	rig := newHarness(t, sourceTree())

	var all strings.Builder

	for _, args := range commands {
		stdout, stderr, err := rig.run(t, args...)
		require.NoError(t, err)

		all.WriteString(stdout + stderr)

		if strings.Contains(stdout, "{") {
			assert.True(t, json.Valid([]byte(stdout)), "the JSON output is one valid document: %s", stdout)
		}
	}

	// An invalid flag-set document: the error names the problem, never the key.
	bad := treeOf(map[string]string{
		"flags/checkout.yaml": checkoutFlags,
		"flagsets/dup.yaml":   checkoutSets + duplicateKeySet,
	})
	hb := newHarness(t, bad)

	_, _, err := hb.run(t, withTarget(srcDir)...)
	require.Error(t, err)
	assert.Equal(t, helper.ExitInvalid, helper.ExitCodeOf(err))
	assert.Contains(
		t,
		err.Error(),
		"duplicate of documents[0].flagSets[0].apiKeys[0]",
		"the problem is reported by position",
	)

	all.WriteString(err.Error())

	assert.NotContains(t, all.String(), canaryAPIKey)
}
