package publish_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/publish"
)

const fileFlagsDoc = `checkout-new:
  variations:
    "on": true
    "off": false
  defaultRule:
    variation: "off"
`

const fileFlagSetDoc = `apiVersion: semaphore/v1
kind: FlagSet
flagSets:
  - name: web
    sources: [checkout]
    apiKeys: [8d3b5f0e-6a4f-4c8e-9c3e-2f2f5d1a7b10]
`

// fileRig is a source tree, an empty store directory and a bootstrap file whose control plane is that directory.
type fileRig struct {
	src    string
	store  string
	config string
}

func newFileRig(t *testing.T) *fileRig {
	t.Helper()

	root := t.TempDir()
	rig := &fileRig{
		src: filepath.Join(
			root,
			"src",
		), store: filepath.Join(root, "store"), config: filepath.Join(root, "bootstrap.yaml"),
	}

	require.NoError(t, os.Mkdir(rig.store, 0o750))
	rig.writeFlags(t, fileFlagsDoc)
	rig.write(t, "flagsets/web.yaml", fileFlagSetDoc)
	require.NoError(t, os.WriteFile(rig.config, []byte(`version: 2
connections:
  control: {type: file, path: `+rig.store+`}
controlPlane:
  connection: control
  manifest: {key: manifests/stable.yaml, channel: stable, pollInterval: 1s, jitter: 0}
`), 0o600))

	return rig
}

func (r *fileRig) write(t *testing.T, rel, content string) {
	t.Helper()

	path := filepath.Join(r.src, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func (r *fileRig) writeFlags(t *testing.T, content string) {
	t.Helper()
	r.write(t, "flags/checkout.yaml", content)
}

// run executes the command and returns its output and the exit code (0 for success).
func (*fileRig) run(t *testing.T, args ...string) (string, int) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	err := publish.Run(t.Context(), args, &helper.Streams{Stdout: &stdout, Stderr: &stderr}, publish.DefaultDeps())
	if err == nil {
		return stdout.String(), 0
	}

	exit, ok := errors.AsType[*helper.ExitError](err)
	require.True(t, ok, "%v", err)

	return stdout.String() + stderr.String() + err.Error(), exit.Code
}

func TestFileStorePublishAndVerify(t *testing.T) {
	t.Parallel()

	rig := newFileRig(t)

	out, code := rig.run(t, "--dry-run", rig.src, "--config", rig.config)
	assert.Zero(t, code, out)
	assert.Contains(t, out, "would write generation 1")

	_, err := os.Stat(filepath.Join(rig.store, "manifests"))
	require.ErrorIs(t, err, os.ErrNotExist, "a dry run writes nothing")

	out, code = rig.run(t, rig.src, "--config", rig.config)
	assert.Zero(t, code, out)
	assert.Contains(t, out, "published generation 1 (was 0) to manifests/stable.yaml, channel stable")

	out, code = rig.run(t, rig.src, "--config", rig.config)
	assert.Zero(t, code, out)
	assert.Contains(t, out, "generation 1")
	assert.NotContains(t, out, "published generation 2")

	rig.writeFlags(t, strings.Replace(fileFlagsDoc, `variation: "off"`, `variation: "on"`, 1))

	out, code = rig.run(t, "-o", "json", rig.src, "--config", rig.config)
	assert.Zero(t, code, out)
	assert.Contains(t, out, `"generation": 2`)

	out, code = rig.run(t, "verify", "--config", rig.config)
	assert.Zero(t, code, out)

	manifest, err := os.ReadFile(filepath.Join(rig.store, "manifests", "stable.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(manifest), "generation: 2")
}

func TestFileStoreVerifyFailsOnADamagedObject(t *testing.T) {
	t.Parallel()

	rig := newFileRig(t)

	_, code := rig.run(t, rig.src, "--config", rig.config)
	require.Zero(t, code)

	var object string

	require.NoError(
		t,
		filepath.WalkDir(filepath.Join(rig.store, "objects"), func(path string, entry os.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".yaml") && object == "" {
				object = path
			}

			return err
		}),
	)
	require.NoError(t, os.WriteFile(object, []byte("damaged"), 0o600))

	out, code := rig.run(t, "verify", "--config", rig.config)
	assert.Equal(t, helper.ExitVerify, code, out)
}

func TestFileStoreTargetErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		store    func(rig *fileRig) string
		name     string
		wantText string
		extra    []string
		wantCode int
	}{
		{
			name: "a root that does not exist", store: func(rig *fileRig) string { return rig.store + "-missing" },
			wantCode: helper.ExitFailure, wantText: "no such file or directory",
		},
		{
			name: "a prefix that leaves the root", store: func(rig *fileRig) string { return rig.store },
			extra: []string{"--prefix", "../outside"}, wantCode: helper.ExitInvalid, wantText: "key refused",
		},
		{
			name: "an absolute prefix", store: func(rig *fileRig) string { return rig.store },
			extra: []string{
				"--prefix",
				"/abs",
			}, wantCode: helper.ExitInvalid, wantText: "prefix must not start with a slash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newFileRig(t)

			doc := "version: 2\nconnections:\n  control: {type: file, path: " + tt.store(rig) + "}\n" +
				"controlPlane:\n  connection: control\n  manifest: {key: manifests/stable.yaml}\n"
			require.NoError(t, os.WriteFile(rig.config, []byte(doc), 0o600))

			out, code := rig.run(t, append([]string{rig.src, "--config", rig.config}, tt.extra...)...)
			assert.Equal(t, tt.wantCode, code, out)
			assert.Contains(t, out, tt.wantText)
			assert.NoDirExists(t, filepath.Join(filepath.Dir(rig.store), "outside"))
		})
	}
}
