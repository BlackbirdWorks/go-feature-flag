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
	"github.com/blackbirdworks/semaphore/controlplane"
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

	stdout, _, err := rig.run(t, withTarget(srcDir, "-o", jsonFlag)...)
	require.Error(t, err)

	var got struct {
		Error struct {
			Code    string   `json:"code"`
			Details []string `json:"details"`
		} `json:"error"`
	}

	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "invalid", got.Error.Code)
	assert.Equal(
		t,
		[]string{"flag checkout-new: validating the flag: invalid query: parsing the query"},
		got.Error.Details,
	)
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
			args:       []string{srcDir, "-o", jsonFlag},
			wantCode:   helper.ExitFailure,
			wantGolden: "error_exit_1.json",
		},
		{
			name:       "invalid",
			args:       withTarget(srcDir, "-o", jsonFlag),
			wantCode:   helper.ExitInvalid,
			wantGolden: "error_exit_2.json",
			tree:       func() fs.FS { return treeOf(map[string]string{badFile: "bad:\n  disable: true\n"}) },
		},
		{
			name:       "conflict",
			args:       withTarget(srcDir, "-o", jsonFlag),
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
			args:       withTarget(srcDir, "-o", jsonFlag),
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
			args:       withTarget(srcDir, "-o", jsonFlag),
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

	stdout, _, err := rig.run(t, withTarget(verifyWord, "-o", jsonFlag)...)
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

func TestUsageFailuresAreJSONInJSONMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		wantInMsg string
		args      []string
	}{
		{name: "no directory", args: []string{"-o", jsonFlag}, wantInMsg: `expected one of "<dir> [flags]", "verify"`},
		{name: "two directories", args: []string{"a", "b", "-o", jsonFlag}, wantInMsg: "unexpected argument b"},
		{
			name:      "arguments for verify",
			args:      []string{verifyWord, "extra", "-o", jsonFlag},
			wantInMsg: "unexpected argument extra",
		},
		{
			name:      "a flag value that does not parse",
			args:      []string{"-o", jsonFlag, srcDir, "--generation", "abc"},
			wantInMsg: "--generation: expected a valid 64 bit uint",
		},
		{name: "an unknown flag", args: []string{"-o", jsonFlag, srcDir, "--nope"}, wantInMsg: "unknown flag"},
		{
			name:      "dry-run rejected on verify",
			args:      []string{verifyWord, "--dry-run", "-o", jsonFlag},
			wantInMsg: "do not apply to verify",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newHarness(t, sourceTree())

			stdout, _, err := rig.run(t, tt.args...)
			require.Error(t, err)
			assert.Equal(t, helper.ExitFailure, helper.ExitCodeOf(err))

			var got struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}

			dec := json.NewDecoder(strings.NewReader(stdout))
			require.NoError(t, dec.Decode(&got), stdout)
			assert.False(t, dec.More(), "one JSON value on stdout")
			assert.Equal(t, "usage", got.Error.Code)
			assert.Contains(t, got.Error.Message, tt.wantInMsg)
			assert.Empty(t, rig.fake.Calls())
		})
	}
}

func TestUsageFailureInTextModeIsNotJSON(t *testing.T) {
	t.Parallel()

	rig := newHarness(t, sourceTree())

	stdout, _, err := rig.run(t)
	require.Error(t, err)
	assert.Empty(t, stdout)
}

// seedEngineBadFlags stores a flag file that passes the structural checks and that the engine rejects (a query
// that does not compile) under its checksum, with a manifest that lists it correctly.
func seedEngineBadFlags(t *testing.T, rig *harness, format string) {
	t.Helper()

	body := badQueryFlags
	if format == "toml" {
		body = "[checkout-new]\n[checkout-new.variations]\non = true\noff = false\n" +
			"[checkout-new.defaultRule]\nvariation = \"off\"\n" +
			"[[checkout-new.targeting]]\nquery = \"beta eq (\"\nvariation = \"on\"\n"
	}

	sum := controlplane.ChecksumOf([]byte(body))
	key, err := controlplane.ObjectKey(sum, format)
	require.NoError(t, err)

	manifest, err := controlplane.MarshalManifest(&controlplane.Manifest{
		Channel: "stable", Generation: 1,
		Objects: []controlplane.ManifestObject{{
			Name: "engine", Kind: controlplane.KindFlags, Connection: "control", Key: key, Format: format,
			Checksum: sum, Size: int64(len(body)),
		}},
	})
	require.NoError(t, err)

	rig.fake.Seed(key, []byte(body))
	rig.fake.Seed(manifestKey, manifest)
}

func TestVerifyRunsTheEngineCheck(t *testing.T) {
	t.Parallel()

	for _, format := range []string{controlplane.FormatYAML, "toml"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			rig := newHarness(t, sourceTree())
			seedEngineBadFlags(t, rig, format)

			stdout, _, err := rig.run(t, withTarget(verifyWord)...)
			require.Error(t, err)
			assert.Equal(t, helper.ExitVerify, helper.ExitCodeOf(err))
			assert.Contains(t, stdout, "problem: engine invalid-content")
			assert.Contains(t, stdout, "flag checkout-new: ", "the engine names the flag, a TOML file is read too")
		})
	}
}

func TestVerifyNamesWhatItDidNotCheck(t *testing.T) {
	t.Parallel()

	rig := newHarness(t, sourceTree())

	manifest, err := controlplane.MarshalManifest(&controlplane.Manifest{
		Channel: "stable", Generation: 1,
		Objects: []controlplane.ManifestObject{{
			Name: "elsewhere", Kind: controlplane.KindFlags, Connection: "other", Key: "k", Format: "yaml",
			Checksum: controlplane.ChecksumOf([]byte("x")), Size: 1,
		}},
	})
	require.NoError(t, err)
	rig.fake.Seed(manifestKey, manifest)

	stdout, _, err := rig.run(t, withTarget(verifyWord)...)
	require.NoError(t, err)
	assert.Contains(t, stdout, "checked 0 objects, skipped 1 (other connections)")
	assert.Contains(t, stdout, "note: 1 object(s) of other connections were not checked: elsewhere")
	assert.Contains(t, stdout, "ok: every checked object matches")
	assert.NotContains(t, stdout, "every listed object")
}

// TestADirectoryNamedVerifyNeedsADotSlash pins the sentence of docs/tooling/publish.md: the word verify is the
// subcommand, ./verify is a directory.
func TestADirectoryNamedVerifyNeedsADotSlash(t *testing.T) {
	t.Parallel()

	const applyWord = "apply"

	tests := []struct {
		name       string
		wantStdout string
		wantFlag   string
		args       []string
		wantCode   int
	}{
		{
			name:     "the word is the subcommand: it verifies a bucket that holds no manifest",
			args:     withTarget(verifyWord),
			wantCode: helper.ExitVerify,
		},
		{
			name:       "with ./ it is a directory to publish",
			args:       withTarget("./verify"),
			wantStdout: publishedGen1,
			wantFlag:   "from-dot-slash-verify",
		},
		{
			name:       "with -- a bare verify is a directory to publish",
			args:       append(withTarget(), "--", verifyWord),
			wantStdout: publishedGen1,
			wantFlag:   "from-bare-verify",
		},
		{
			name:       "apply is a directory to publish, not a reserved subcommand",
			args:       withTarget(applyWord),
			wantStdout: publishedGen1,
			wantFlag:   "from-apply-dir",
		},
		{
			name:       "short -c=bootstrap strips equals sign",
			args:       []string{"-c=" + cfgOK, srcDir},
			wantStdout: publishedGen1,
			wantFlag:   checkoutSetsFlag,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newHarness(t, sourceTree())
			rig.deps.ReadFile = func(path string) ([]byte, error) {
				if path == cfgOK {
					return []byte("version: 2\nconnections:\n  control: {type: s3, region: us-east-1, bucket: acme}\n" +
						"controlPlane:\n  connection: control\n  manifest: {key: manifests/stable.yaml}\n"), nil
				}

				return nil, fs.ErrNotExist
			}
			// "./verify" and "verify" and "apply" are distinct from "src": what is published says which was opened.
			rig.trees["./verify"] = treeOf(map[string]string{"flags/from-dot-slash-verify.yaml": checkoutFlags})
			rig.trees[verifyWord] = treeOf(map[string]string{"flags/from-bare-verify.yaml": checkoutFlags})
			rig.trees[applyWord] = treeOf(map[string]string{"flags/from-apply-dir.yaml": checkoutFlags})

			stdout, _, err := rig.run(t, tt.args...)

			if tt.wantCode == 0 {
				require.NoError(t, err)
				assert.Contains(t, stdout, tt.wantStdout)
				assert.Contains(t, stdout, tt.wantFlag)

				if tt.wantFlag != checkoutSetsFlag {
					assert.NotContains(t, stdout, checkoutSetsFlag, "the tree of src was not read")
				}

				return
			}

			require.Error(t, err)
			assert.Equal(t, tt.wantCode, helper.ExitCodeOf(err))
			assert.Empty(t, rig.fake.Keys(), "nothing is published")
		})
	}
}
