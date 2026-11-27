package publish_test

import (
	"bytes"
	"context"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/publish"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/testhelpers/s3fake"
)

const (
	secretKeyPath = "/run/secret-key"
	flagsB        = "flags/b.yaml"
	jsonFlag      = "json"
	bootstrapPath = "/etc/bootstrap.yaml"
	opOpen        = "open"
	optChannel    = "--channel"
	optEndpoint   = "--endpoint"
	optPrefix     = "--prefix"
	chanCanary    = "canary"
	connEU        = "flags-eu"
	verifyWord    = "verify"
	badFile       = "flags/bad.yaml"
	regionUSEast  = "us-east-1"
	srcDir        = "src"
	flagsA        = "flags/a.yaml"
	optConfig     = "--config"
	optBucket     = "--bucket"
	optRegion     = "--region"
	envKeyID      = "PUBLISH_KEY_ID"
	cfgOK         = "/ok.yaml"
	cfgV1         = "/v1.yaml"
	cfgUnknown    = "/unknown.yaml"

	// canaryAPIKey is an API key of a flag-set document: it must never appear in any output.
	canaryAPIKey = "canary-api-key-7f3a9c"
	// canarySecret is a connection credential: it must never appear in any output.
	canarySecret = "canary-secret-access-key-5d1e"
	canaryKeyID  = "CANARYACCESSKEYID"

	manifestKey = "manifests/stable.yaml"

	checkoutFlags = `checkout-new:
  variations:
    "on": true
    "off": false
  defaultRule:
    variation: "off"
`
	checkoutSets = `apiVersion: semaphore/v1
kind: FlagSet
flagSets:
  - name: checkout
    sources: [checkout]
    apiKeys: [` + canaryAPIKey + `]
`
)

// sourceTree is a valid source directory.
func sourceTree() fstest.MapFS {
	return fstest.MapFS{
		"flags/checkout.yaml":         {Data: []byte(checkoutFlags)},
		"flagsets/checkout-sets.yaml": {Data: []byte(checkoutSets)},
		"flagsets/.gitkeep":           {Data: nil},
		".hidden/flags/skip.yaml":     {Data: []byte("not read")},
	}
}

// harness is a command wired to an in-memory bucket and a source tree.
type harness struct {
	fake *s3fake.Fake
	// trees are the source directories that exist, by the path the command is given; any other path does not exist.
	trees map[string]fs.FS
	deps  publish.Deps
	// opened lists every path the command asked to open as a source directory, in order.
	opened []string
	// specs are the connection specs the command asked a client for.
	specs []*connection.S3Spec
	names []string
}

func newHarness(t *testing.T, tree fs.FS) *harness {
	t.Helper()

	rig := &harness{fake: s3fake.New(manifestKey), trees: map[string]fs.FS{srcDir: tree}}
	rig.fake.Bucket = "acme" // the bucket that withTarget names: a call for another one is a missing bucket

	rig.deps = publish.DefaultDeps()
	rig.deps.OpenDir = func(dir string) fs.FS {
		rig.opened = append(rig.opened, dir)

		if served, ok := rig.trees[dir]; ok {
			return served
		}

		return missingDir{dir: dir}
	}
	rig.deps.NewAPI = func(_ context.Context, spec *connection.S3Spec, name string) (s3publish.API, error) {
		rig.specs = append(rig.specs, spec)
		rig.names = append(rig.names, name)

		return rig.fake, nil
	}

	return rig
}

// missingDir is a directory that does not exist.
type missingDir struct{ dir string }

func (m missingDir) Open(string) (fs.File, error) {
	return nil, &fs.PathError{Op: opOpen, Path: m.dir, Err: fs.ErrNotExist}
}

// sourceDirArg returns the source directory that args name (the first argument of publish that is not a flag, a
// flag value or the subcommand verify), or "".
func sourceDirArg(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == verifyWord:
			return ""
		case strings.HasPrefix(arg, "-"):
			if !strings.Contains(arg, "=") && takesValue(arg) && i+1 < len(args) {
				i++
			}
		default:
			return arg
		}
	}

	return ""
}

func takesValue(flag string) bool {
	switch flag {
	case "-o", "--output", "-c", "--config", "--bucket", optPrefix, "--region", optEndpoint, optChannel,
		"--connection", "--manifest-key", "--generation":
		return true
	}

	return false
}

// run executes publish with args and returns stdout, stderr and the error. It also checks which source directory
// was opened: at most the one that args name, and exactly that one when the command succeeded.
func (rig *harness) run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	deps := rig.deps
	deps.Args = func() []string { return args }

	cmd := publish.NewPublishCmdWith(deps)

	var stdout, stderr bytes.Buffer

	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)

	before := len(rig.opened)
	err := cmd.ExecuteContext(t.Context())

	assertOpened(t, sourceDirArg(args), rig.opened[before:], err == nil)

	return stdout.String(), stderr.String(), err
}

// assertOpened checks the source directories that one run opened: none when args name none, and otherwise at most
// the one they name, which a successful run must have opened.
func assertOpened(t *testing.T, want string, opened []string, succeeded bool) {
	t.Helper()

	switch {
	case want == "":
		assert.Empty(t, opened, "no source directory was asked for")
	case succeeded:
		assert.Equal(t, []string{want}, opened, "the directory named on the command line was opened, and only it")
	default:
		for _, dir := range opened {
			assert.Equal(t, want, dir, "a failed run opened another directory than the one named")
		}
	}
}

func withTarget(args ...string) []string {
	return slices.Concat(args, []string{optBucket, "acme", optRegion, regionUSEast})
}

func treeOf(files map[string]string) fstest.MapFS {
	tree := fstest.MapFS{}
	for name, data := range files {
		tree[name] = &fstest.MapFile{Data: []byte(data)}
	}

	return tree
}

// apiError is an S3 error with a code, as the SDK returns it.
func apiError(code string) error { return s3fake.APIError(code) }

// publishOnce publishes the current tree with extra arguments and fails the test on an error.
func (rig *harness) publishOnce(t *testing.T, extra ...string) {
	t.Helper()

	_, _, err := rig.run(t, withTarget(append([]string{srcDir}, extra...)...)...)
	require.NoError(t, err)
}
