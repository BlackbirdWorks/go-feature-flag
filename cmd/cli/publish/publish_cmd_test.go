package publish_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

func golden(t *testing.T, name string) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)

	return string(b)
}

func TestPublishOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		wantGolden string
		args       []string
		republish  bool
	}{
		{name: "text, first publish", args: withTarget(srcDir), wantGolden: "publish_first.txt"},
		{name: "json, first publish", args: withTarget(srcDir, "-o", "json"), wantGolden: "publish_first.json"},
		{name: "text, dry run", args: withTarget(srcDir, "--dry-run"), wantGolden: "publish_dry_run.txt"},
		{
			name:       "json, dry run",
			args:       withTarget(srcDir, "--dry-run", "-o", "json"),
			wantGolden: "publish_dry_run.json",
		},
		{name: "text, nothing changed", args: withTarget(srcDir), republish: true, wantGolden: "publish_no_change.txt"},
		{
			name:       "json, nothing changed",
			args:       withTarget(srcDir, "-o", "json"),
			republish:  true,
			wantGolden: "publish_no_change.json",
		},
		{
			name: "text, prefix channel and manifest key", wantGolden: "publish_prefix.txt",
			args: withTarget(srcDir, optPrefix, "acme", "--channel", chanCanary, "--connection", connEU),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newHarness(t, sourceTree())

			if tt.republish {
				_, _, err := rig.run(t, withTarget(srcDir)...)
				require.NoError(t, err)
			}

			stdout, stderr, err := rig.run(t, tt.args...)
			require.NoError(t, err)

			assert.Equal(t, golden(t, tt.wantGolden), stdout)
			assert.Empty(t, stderr)
		})
	}
}

func TestPublishCreatedAtComesFromTheClock(t *testing.T) {
	t.Parallel()

	rig := newHarness(t, sourceTree())
	_, _, err := rig.run(t, withTarget(srcDir)...)
	require.NoError(t, err)

	body, ok := rig.fake.Body(manifestKey)
	require.True(t, ok)
	assert.Contains(t, string(body), "createdAt: 2025-06-15T12:00:00Z\n")
}

func TestPublishExitCodes(t *testing.T) {
	t.Parallel()

	const (
		badFlags   = "bad:\n  disable: true\n"
		brokenYAML = "flags:\n  - [unterminated\n"
	)

	tests := []struct {
		setup       func(rig *harness)
		tree        func() map[string]string
		name        string
		wantInErr   string
		wantAlso    string
		args        []string
		wantCode    int
		wantNoWrite bool
	}{
		{name: "success", args: withTarget(srcDir), wantCode: 0},
		{
			name:      "no target",
			args:      []string{srcDir},
			wantCode:  helper.ExitFailure,
			wantInErr: "give --config, or --bucket and --region",
		},
		{
			name:      "bad output",
			args:      withTarget(srcDir, "-o", "xml"),
			wantCode:  helper.ExitFailure,
			wantInErr: "--output must be text or json",
		},
		{
			name: "invalid flag file", args: withTarget(srcDir), wantCode: helper.ExitInvalid, wantNoWrite: true,
			tree:      func() map[string]string { return map[string]string{badFile: badFlags} },
			wantInErr: "bad (src/flags/bad.yaml)",
		},
		{
			name:        "invalid YAML names the file and the line",
			args:        withTarget(srcDir),
			wantCode:    helper.ExitInvalid,
			wantNoWrite: true,
			tree:        func() map[string]string { return map[string]string{"flags/broken.yaml": brokenYAML} },
			wantInErr:   "broken (src/flags/broken.yaml)",
			wantAlso:    "(line 1)",
		},
		{
			name:        "unknown file",
			args:        withTarget(srcDir),
			wantCode:    helper.ExitInvalid,
			wantNoWrite: true,
			tree:        func() map[string]string { return map[string]string{flagsA: checkoutFlags, "notes.txt": "x"} },
			wantInErr:   `unknown file "src/notes.txt"`,
		},
		{
			name:      "generation does not increase",
			args:      withTarget(srcDir, "--generation", "2"),
			wantCode:  helper.ExitInvalid,
			setup:     func(rig *harness) { rig.publishOnce(t, "--generation", "5") },
			tree:      func() map[string]string { return map[string]string{"flags/other.yaml": checkoutFlags} },
			wantInErr: "generation does not increase",
		},
		{
			name: "lost race", args: withTarget(srcDir), wantCode: helper.ExitConflict,
			setup: func(rig *harness) {
				rig.fake.Before = func(call string) error {
					if call == "PUT "+manifestKey+" create" {
						rig.fake.Seed(manifestKey, []byte("rival"))
					}

					return nil
				}
			},
			wantInErr: "the manifest changed since it was read",
		},
		{
			name: "store call fails", args: withTarget(srcDir), wantCode: helper.ExitTransport, wantNoWrite: true,
			setup: func(rig *harness) {
				rig.fake.Before = func(call string) error {
					if call == "GET "+manifestKey {
						return apiError("ServiceUnavailable")
					}

					return nil
				}
			},
			wantInErr: "GetObject",
		},
		{
			name: "manifest in the bucket is broken", args: withTarget(srcDir), wantCode: helper.ExitVerify,
			setup:     func(rig *harness) { rig.fake.Seed(manifestKey, []byte("not a manifest")) },
			wantInErr: "not readable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tree := sourceTree()
			if tt.tree != nil {
				tree = treeOf(tt.tree())
			}

			rig := newHarness(t, tree)
			if tt.setup != nil {
				tt.setup(rig)
			}

			before := rig.fake.Keys()

			_, _, err := rig.run(t, tt.args...)

			if tt.wantCode == 0 {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)
			assert.Equal(t, tt.wantCode, helper.ExitCodeOf(err), err.Error())
			assert.Contains(t, err.Error(), tt.wantInErr)
			assert.Contains(t, err.Error(), tt.wantAlso)

			if tt.wantNoWrite {
				assert.Equal(t, before, rig.fake.Keys(), "nothing was written")
			}
		})
	}
}
