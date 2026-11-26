//go:build docker

package publish_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/publish"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

// counting counts the requests that the command's S3 client sends, by method.
type counting struct {
	put atomic.Int32
	get atomic.Int32
}

func (c *counting) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.Method {
	case http.MethodPut:
		c.put.Add(1)
	case http.MethodGet:
		c.get.Add(1)
	}

	return http.DefaultTransport.RoundTrip(req)
}

const e2eFlagsA = `checkout-new:
  variations:
    "on": true
    "off": false
  defaultRule:
    variation: "off"
`

// e2eEnv is a source directory, a bootstrap file for a fresh bucket on the emulator, and the deps of the command.
type e2eEnv struct {
	srv    *gopherstack.Server
	client *s3.Client
	count  *counting
	dir    string
	config string
	bucket string
	deps   publish.Deps
}

func newE2E(t *testing.T) *e2eEnv {
	t.Helper()

	srv := gopherstack.Connect(t)
	bucket := srv.CreateBucket(t, "cli")
	root := t.TempDir()

	for name, content := range map[string]string{
		"src/flags/checkout.yaml":         e2eFlagsA,
		"src/flagsets/checkout-sets.yaml": checkoutSets,
		"secrets/key-id":                  gopherstack.AccessKey,
		"secrets/secret":                  gopherstack.SecretKey + "\n",
	} {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}

	config := filepath.Join(root, "bootstrap.yaml")
	require.NoError(t, os.WriteFile(config, []byte(`version: 2
connections:
  control:
    type: s3
    region: `+gopherstack.Region+`
    endpoint: `+srv.Endpoint+`
    bucket: `+bucket+`
    pathStyle: true
    credentials:
      type: static
      accessKeyId: {fromFile: `+filepath.Join(root, "secrets", "key-id")+`}
      secretAccessKey: {fromFile: `+filepath.Join(root, "secrets", "secret")+`}
controlPlane:
  connection: control
  manifest: {key: manifests/stable.yaml}
`), 0o600))

	count := &counting{}
	deps := publish.DefaultDeps()
	deps.ConnectionOptions = []connection.Option{connection.WithRoundTripper(count)}

	return &e2eEnv{
		srv: srv, client: srv.S3Client(t), count: count, deps: deps, bucket: bucket,
		dir: filepath.Join(root, srcDir), config: config,
	}
}

func runCLI(t *testing.T, deps publish.Deps, args ...string) (string, error) {
	t.Helper()

	cmd := publish.NewPublishCmdWith(deps)

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(t.Context())

	return out.String(), err
}

func (e *e2eEnv) write(t *testing.T, rel, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(e.dir, rel), []byte(content), 0o600))
}

func (e *e2eEnv) readManifest(t *testing.T) *controlplane.Manifest {
	t.Helper()

	m, _, err := controlplane.UnmarshalManifest(e.get(t, manifestKey))
	require.NoError(t, err)
	require.NoError(t, controlplane.ValidateManifest(m))

	return m
}

func (e *e2eEnv) get(t *testing.T, key string) []byte {
	t.Helper()

	return e.srv.GetObject(t, e.bucket, key)
}

func TestE2EPublishDirectoryRepublishAndVerify(t *testing.T) {
	t.Parallel()

	env := newE2E(t)

	out, err := runCLI(t, env.deps, env.dir, optConfig, env.config, "-o", "json")
	require.NoError(t, err, out)

	var first struct {
		Requests struct {
			Put int `json:"put"`
		} `json:"requests"`
		Generation uint64 `json:"generation"`
		Uploaded   int    `json:"uploaded"`
	}

	require.NoError(t, json.Unmarshal([]byte(out), &first))
	assert.Equal(t, uint64(1), first.Generation)
	assert.Equal(t, 2, first.Uploaded)
	assert.Equal(t, 3, first.Requests.Put)
	assert.Equal(
		t,
		int32(3),
		env.count.put.Load(),
		"the counted PUTs are the reported ones: two objects and the manifest",
	)

	manifest := env.readManifest(t)
	assert.Equal(t, uint64(1), manifest.Generation)
	assert.Equal(t, "stable", manifest.Channel)
	require.Len(t, manifest.Objects, 2)

	wantBodies := map[string]string{"checkout": e2eFlagsA, "checkout-sets": checkoutSets}

	for _, obj := range manifest.Objects {
		body := env.get(t, obj.Key)
		assert.Equal(t, wantBodies[obj.Name], string(body), obj.Name)
		require.NoError(t, controlplane.VerifyChecksum(body, obj.Checksum))
		assert.Equal(t, int64(len(body)), obj.Size)
		assert.Equal(t, "control", obj.Connection)
	}

	assert.NotContains(t, string(env.get(t, manifestKey)), gopherstack.SecretKey+"\n", "no credential in the manifest")

	// One changed document: exactly the new object and the manifest are written.
	env.count.put.Store(0)
	env.write(
		t,
		"flags/checkout.yaml",
		e2eFlagsA+"checkout-extra:\n  variations: {a: 1, b: 2}\n  defaultRule: {variation: a}\n",
	)

	out, err = runCLI(t, env.deps, env.dir, optConfig, env.config)
	require.NoError(t, err, out)
	assert.Contains(t, out, "published generation 2 (was 1)")
	assert.Equal(t, int32(2), env.count.put.Load())
	assert.Equal(t, uint64(2), env.readManifest(t).Generation)

	// Same content again: no write.
	env.count.put.Store(0)

	out, err = runCLI(t, env.deps, env.dir, optConfig, env.config)
	require.NoError(t, err, out)
	assert.Contains(t, out, "no change")
	assert.Zero(t, env.count.put.Load())

	// verify passes ...
	out, err = runCLI(t, env.deps, "verify", optConfig, env.config)
	require.NoError(t, err, out)
	assert.Contains(t, out, "ok: every listed object matches")

	// ... then fails once an object is corrupted out of band, naming it.
	victim := env.readManifest(t).Objects[0]
	corrupted := bytes.Clone(env.get(t, victim.Key))
	corrupted[0] ^= 0x01

	_, err = env.client.PutObject(t.Context(), &s3.PutObjectInput{
		Bucket: aws.String(env.bucket), Key: aws.String(victim.Key), Body: bytes.NewReader(corrupted),
	})
	require.NoError(t, err)

	out, err = runCLI(t, env.deps, "verify", optConfig, env.config)
	require.Error(t, err)
	assert.Equal(t, helper.ExitVerify, helper.ExitCodeOf(err))
	assert.Contains(t, out, "problem: "+victim.Name+" checksum-mismatch "+victim.Key)
}

// sharedGate holds each manifest read until both publishers have read, so that both see the same state.
type sharedGate struct {
	release chan struct{}
	arrived atomic.Int32
	once    sync.Once
}

// gatedAPI is the client of one publisher behind the shared gate.
type gatedAPI struct {
	s3publish.API

	gate *sharedGate
}

func (g *gatedAPI) GetObject(
	ctx context.Context,
	in *s3.GetObjectInput,
	opts ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	out, err := g.API.GetObject(ctx, in, opts...)

	if aws.ToString(in.Key) == manifestKey {
		if g.gate.arrived.Add(1) == 2 {
			g.gate.once.Do(func() { close(g.gate.release) })
		}

		select {
		case <-g.gate.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			return nil, context.DeadlineExceeded
		}
	}

	return out, err
}

func TestE2ETwoConcurrentPublishesOneConflicts(t *testing.T) {
	t.Parallel()

	env := newE2E(t)
	gate := &sharedGate{release: make(chan struct{})}

	deps := env.deps
	deps.NewAPI = func(ctx context.Context, spec *connection.S3Spec, name string) (s3publish.API, error) {
		factory := connection.New()

		err := factory.Set(name, spec)
		if err != nil {
			return nil, err
		}

		client, err := factory.S3(ctx, name)
		if err != nil {
			return nil, err
		}

		return &gatedAPI{API: client, gate: gate}, nil
	}

	// Two different source trees for one channel.
	other := filepath.Join(filepath.Dir(env.dir), "src2")
	require.NoError(t, os.MkdirAll(filepath.Join(other, "flags"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(other, "flags", "other.yaml"), []byte(e2eFlagsA), 0o600))

	errs := make([]error, 2)
	outs := make([]string, 2)

	var wg sync.WaitGroup

	for i, dir := range []string{env.dir, other} {
		wg.Go(func() {
			cmd := publish.NewPublishCmdWith(deps)

			var out bytes.Buffer

			cmd.SetOut(&out)
			cmd.SetArgs([]string{dir, optConfig, env.config})
			errs[i] = cmd.ExecuteContext(t.Context())
			outs[i] = out.String()
		})
	}

	wg.Wait()

	codes := []int{codeOf(errs[0]), codeOf(errs[1])}
	assert.ElementsMatch(
		t,
		[]int{0, helper.ExitConflict},
		codes,
		"one publisher wins, the other gets the conflict code: %v %v",
		errs[0],
		errs[1],
	)

	manifest := env.readManifest(t)
	assert.Equal(t, uint64(1), manifest.Generation)

	for _, o := range manifest.Objects {
		require.NoError(t, controlplane.VerifyChecksum(env.get(t, o.Key), o.Checksum))
	}
}

func codeOf(err error) int {
	if err == nil {
		return 0
	}

	return helper.ExitCodeOf(err)
}

//nolint:paralleltest // t.Setenv: the credentials of this form come from the AWS environment of the process
func TestE2EExplicitFlagsTarget(t *testing.T) {
	env := newE2E(t)
	env.srv.SetAwsEnv(t)

	args := []string{
		env.dir,
		optBucket,
		env.bucket,
		optRegion,
		gopherstack.Region,
		"--endpoint",
		env.srv.Endpoint,
		"--prefix",
		"p",
		"--channel",
		"canary",
	}

	out, err := runCLI(t, env.deps, args...)
	require.NoError(t, err, out)
	assert.Contains(t, out, "published generation 1 (was 0) to p/manifests/canary.yaml, channel canary")

	out, err = runCLI(
		t,
		env.deps,
		"verify",
		optBucket,
		env.bucket,
		optRegion,
		gopherstack.Region,
		"--endpoint",
		env.srv.Endpoint,
		"--prefix",
		"p",
		"--channel",
		"canary",
	)
	require.NoError(t, err, out)

	raw := env.get(t, "p/manifests/canary.yaml")
	manifest, _, err := controlplane.UnmarshalManifest(raw)
	require.NoError(t, err)
	assert.Equal(t, "canary", manifest.Channel)
	assert.Equal(t, "control", manifest.Objects[0].Connection)
	assert.Contains(t, manifest.Objects[0].Key, "p/objects/sha256/")
}
