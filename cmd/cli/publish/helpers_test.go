package publish_test

import (
	"bytes"
	"context"
	"io/fs"
	"slices"
	"testing"
	"testing/fstest"

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
	deps publish.Deps
	// specs are the connection specs the command asked a client for.
	specs []*connection.S3Spec
	names []string
}

func newHarness(t *testing.T, tree fs.FS) *harness {
	t.Helper()

	rig := &harness{fake: s3fake.New(manifestKey)}

	rig.deps = publish.DefaultDeps()
	rig.deps.OpenDir = func(string) fs.FS { return tree }
	rig.deps.NewAPI = func(_ context.Context, spec *connection.S3Spec, name string) (s3publish.API, error) {
		rig.specs = append(rig.specs, spec)
		rig.names = append(rig.names, name)

		return rig.fake, nil
	}

	return rig
}

// run executes publish with args and returns stdout, stderr and the error.
func (rig *harness) run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	cmd := publish.NewPublishCmdWith(rig.deps)

	var stdout, stderr bytes.Buffer

	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(t.Context())

	return stdout.String(), stderr.String(), err
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
