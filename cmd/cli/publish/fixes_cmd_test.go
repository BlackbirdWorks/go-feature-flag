package publish_test

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

const badQueryFlags = `checkout-new:
  variations: {"on": true, "off": false}
  defaultRule: {variation: "off"}
  targeting:
    - query: 'beta eq ('
      variation: "on"
`

func TestPublishRefusesFlagsThatTheEngineRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		body     string
		wantFlag string
	}{
		{name: "a query that does not compile", body: badQueryFlags, wantFlag: "flag checkout-new: "},
		{
			name: "a rule with an unknown variation (caught by the structural check)",
			body: "other-flag:\n  variations: {a: 1, b: 2}\n  defaultRule: {variation: a}\n" +
				"  targeting:\n    - query: 'x eq 1'\n      variation: zzz\n",
			wantFlag: "other-flag.targeting[0].variation",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newHarness(t, treeOf(map[string]string{"flags/engine.yaml": tt.body}))

			_, _, err := rig.run(t, withTarget(srcDir)...)
			require.Error(t, err)
			assert.Equal(t, helper.ExitInvalid, helper.ExitCodeOf(err))
			assert.Contains(t, err.Error(), "engine (src/flags/engine.yaml)", "the file")
			assert.Contains(t, err.Error(), tt.wantFlag, "the flag")
			assert.Empty(t, rig.fake.Calls(), "nothing is read or written")
		})
	}
}

func TestPublishJSONErrorListsTheFlagsInDetails(t *testing.T) {
	t.Parallel()

	rig := newHarness(t, treeOf(map[string]string{"flags/engine.yaml": badQueryFlags}))

	stdout, _, err := rig.run(t, withTarget(srcDir, "-o", "json")...)
	require.Error(t, err)

	var got struct {
		Error struct {
			Code    string   `json:"code"`
			Message string   `json:"message"`
			Details []string `json:"details"`
		} `json:"error"`
	}

	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "invalid", got.Error.Code)
	require.Len(t, got.Error.Details, 1)
	assert.True(t, strings.HasPrefix(got.Error.Details[0], "flag checkout-new: "), got.Error.Details[0])
}

func TestJSONErrorGoldenPerExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setup      func(rig *harness)
		tree       func() fs.FS
		name       string
		wantGolden string
		args       []string
		wantCode   int
	}{
		{
			name:       "usage",
			args:       []string{srcDir, "-o", "json"},
			wantCode:   helper.ExitFailure,
			wantGolden: "error_exit_1.json",
		},
		{
			name:       "invalid",
			args:       withTarget(srcDir, "-o", "json"),
			wantCode:   helper.ExitInvalid,
			wantGolden: "error_exit_2.json",
			tree:       func() fs.FS { return treeOf(map[string]string{badFile: "bad:\n  disable: true\n"}) },
		},
		{
			name:       "conflict",
			args:       withTarget(srcDir, "-o", "json"),
			wantCode:   helper.ExitConflict,
			wantGolden: "error_exit_3.json",
			setup: func(rig *harness) {
				rig.fake.Before = func(call string) error {
					if call == "PUT "+manifestKey+" create" {
						rig.fake.Seed(manifestKey, []byte("rival"))
					}

					return nil
				}
			},
		},
		{
			name:       "transport",
			args:       withTarget(srcDir, "-o", "json"),
			wantCode:   helper.ExitTransport,
			wantGolden: "error_exit_4.json",
			setup: func(rig *harness) {
				rig.fake.Before = func(call string) error {
					if call == "GET "+manifestKey {
						return apiError("ServiceUnavailable")
					}

					return nil
				}
			},
		},
		{
			name:       "bad state",
			args:       withTarget(srcDir, "-o", "json"),
			wantCode:   helper.ExitVerify,
			wantGolden: "error_exit_5.json",
			setup:      func(rig *harness) { rig.fake.Seed(manifestKey, []byte("not a manifest")) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var tree fs.FS = sourceTree()
			if tt.tree != nil {
				tree = tt.tree()
			}

			rig := newHarness(t, tree)
			if tt.setup != nil {
				tt.setup(rig)
			}

			stdout, _, err := rig.run(t, tt.args...)
			require.Error(t, err)
			assert.Equal(t, tt.wantCode, helper.ExitCodeOf(err))
			assert.Equal(t, golden(t, tt.wantGolden), stdout)
			assert.True(t, json.Valid([]byte(stdout)))
			assert.NotContains(t, stdout, canaryAPIKey)
		})
	}
}

func TestTextModeKeepsTheErrorOffStdout(t *testing.T) {
	t.Parallel()

	rig := newHarness(t, treeOf(map[string]string{badFile: "bad:\n  disable: true\n"}))

	stdout, _, err := rig.run(t, withTarget(srcDir)...)
	require.Error(t, err)
	assert.Empty(t, stdout, "in text mode the error is only the returned error")
}

func TestVerifyFailureJSONIsTheReportAlone(t *testing.T) {
	t.Parallel()

	rig := newHarness(t, sourceTree())
	rig.publishOnce(t)
	rig.fake.Remove(checkoutKey)

	stdout, _, err := rig.run(t, withTarget("verify", "-o", "json")...)
	require.Error(t, err)
	assert.Equal(t, helper.ExitVerify, helper.ExitCodeOf(err))

	dec := json.NewDecoder(strings.NewReader(stdout))

	var report map[string]any

	require.NoError(t, dec.Decode(&report))
	assert.False(t, dec.More(), "a single JSON document")
	assert.Equal(t, false, report["ok"])
	assert.NotContains(t, stdout, `"error"`)
}

func TestPublishRefusesNonRegularFilesAndHugeFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		files     fstest.MapFS
		wantInErr string
	}{
		{
			name: "a symbolic link",
			files: fstest.MapFS{
				"flags/a.yaml":    {Data: []byte(checkoutFlags)},
				"flags/link.yaml": {Mode: fs.ModeSymlink, Data: []byte("a.yaml")},
			},
			wantInErr: `not a regular file "src/flags/link.yaml"`,
		},
		{
			name:      "a file over the 16 MiB limit is refused before it is read",
			files:     fstest.MapFS{"flags/huge.yaml": {Data: make([]byte, 16<<20+1)}},
			wantInErr: `file is over the size limit "src/flags/huge.yaml"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newHarness(t, tt.files)

			_, _, err := rig.run(t, withTarget(srcDir)...)
			require.Error(t, err)
			assert.Equal(t, helper.ExitInvalid, helper.ExitCodeOf(err))
			assert.Contains(t, err.Error(), tt.wantInErr)
			assert.Empty(t, rig.fake.Calls())
		})
	}
}

func TestReplaceInvalidManifestFlag(t *testing.T) {
	t.Parallel()

	rig := newHarness(t, sourceTree())
	rig.fake.Seed(manifestKey, []byte("not a manifest"))

	_, _, err := rig.run(t, withTarget(srcDir)...)
	require.Error(t, err)
	assert.Equal(t, helper.ExitVerify, helper.ExitCodeOf(err))
	assert.Contains(t, err.Error(), "--replace-invalid-manifest")

	_, _, err = rig.run(t, withTarget(srcDir, "--replace-invalid-manifest")...)
	require.Error(t, err)
	assert.Equal(t, helper.ExitInvalid, helper.ExitCodeOf(err), "the flag needs --generation")
	assert.Contains(t, err.Error(), "explicit generation")

	stdout, _, err := rig.run(t, withTarget(srcDir, "--replace-invalid-manifest", "--generation", "5")...)
	require.NoError(t, err)
	assert.Contains(t, stdout, "published generation 5 (was 0)")

	stdout, _, err = rig.run(t, withTarget(srcDir)...)
	require.NoError(t, err, "the channel works again without the flag")
	assert.Contains(t, stdout, "no change")
}

func TestNoChangeHealsAMissingObjectAndSaysSo(t *testing.T) {
	t.Parallel()

	rig := newHarness(t, sourceTree())
	rig.publishOnce(t)
	rig.fake.Remove(checkoutKey)

	stdout, _, err := rig.run(t, withTarget(srcDir, "--dry-run")...)
	require.NoError(t, err)
	assert.Contains(
		t,
		stdout,
		"generation 1; 1 missing object(s) would be uploaded again",
	)
	assert.Contains(t, stdout, "would-upload")

	stdout, _, err = rig.run(t, withTarget(srcDir)...)
	require.NoError(t, err)
	assert.Contains(
		t,
		stdout,
		"generation 1; 1 missing object(s) uploaded again",
	)
	assert.Contains(t, stdout, "uploaded")

	_, found := rig.fake.Body(checkoutKey)
	assert.True(t, found)
	rig.fake.AssertConsistent(t)
}
