//go:build docker

package publish_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
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

	var out bytes.Buffer

	err := publish.Run(t.Context(), args, &helper.Streams{Stdout: &out, Stderr: &out}, deps)

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

	wantBodies := map[string]string{"checkout": e2eFlagsA, checkoutSetsFlag: checkoutSets}

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
	out, err = runCLI(t, env.deps, verifyWord, optConfig, env.config)
	require.NoError(t, err, out)
	assert.Contains(t, out, "ok: every checked object matches")

	// ... then fails once an object is corrupted out of band, naming it.
	victim := env.readManifest(t).Objects[0]
	corrupted := bytes.Clone(env.get(t, victim.Key))
	corrupted[0] ^= 0x01

	_, err = env.client.PutObject(t.Context(), &s3.PutObjectInput{
		Bucket: aws.String(env.bucket), Key: aws.String(victim.Key), Body: bytes.NewReader(corrupted),
	})
	require.NoError(t, err)

	out, err = runCLI(t, env.deps, verifyWord, optConfig, env.config)
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
			var out bytes.Buffer

			errs[i] = publish.Run(
				t.Context(),
				[]string{dir, optConfig, env.config},
				&helper.Streams{Stdout: &out, Stderr: &out},
				deps,
			)
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
		optEndpoint,
		env.srv.Endpoint,
		optPrefix,
		"p",
		optChannel,
		chanCanary,
	}

	out, err := runCLI(t, env.deps, args...)
	require.NoError(t, err, out)
	assert.Contains(t, out, "published generation 1 (was 0) to p/manifests/canary.yaml, channel canary")

	out, err = runCLI(
		t,
		env.deps,
		verifyWord,
		optBucket,
		env.bucket,
		optRegion,
		gopherstack.Region,
		optEndpoint,
		env.srv.Endpoint,
		optPrefix,
		"p",
		optChannel,
		chanCanary,
	)
	require.NoError(t, err, out)

	raw := env.get(t, "p/manifests/canary.yaml")
	manifest, _, err := controlplane.UnmarshalManifest(raw)
	require.NoError(t, err)
	assert.Equal(t, chanCanary, manifest.Channel)
	assert.Equal(t, "control", manifest.Objects[0].Connection)
	assert.Contains(t, manifest.Objects[0].Key, "p/objects/sha256/")
}

const e2eChannelName = "stable"

const e2eSegments = `apiVersion: semaphore/v1
kind: Segments
segments:
  - name: beta
    members:
      - alice
  - name: churned
    connection: audiences
    version:
      key: churned:version
      interval: 5s
`

const e2eSegmentSets = `apiVersion: semaphore/v1
kind: FlagSet
flagSets:
  - name: with-segments
    sources: [checkout]
    segmentSources: [people]
    apiKeys: [segment-api-key-0001]
`

func TestE2EPublishSegmentsAndVerify(t *testing.T) {
	t.Parallel()

	env := newE2E(t)
	require.NoError(t, os.MkdirAll(filepath.Join(env.dir, "segments"), 0o750))
	env.write(t, "segments/people.yaml", e2eSegments)
	env.write(t, "flagsets/segment-sets.yaml", e2eSegmentSets)

	out, err := runCLI(t, env.deps, env.dir, optConfig, env.config)
	require.NoError(t, err, out)

	manifest := env.readManifest(t)
	require.Len(t, manifest.Objects, 4)

	var found *controlplane.ManifestObject

	for i := range manifest.Objects {
		if manifest.Objects[i].Kind == controlplane.KindSegments {
			found = &manifest.Objects[i]
		}
	}

	require.NotNil(t, found, "the manifest lists the segments object")
	assert.Equal(t, "people", found.Name)
	assert.Equal(t, e2eSegments, string(env.get(t, found.Key)))
	require.NoError(t, controlplane.VerifyChecksum(env.get(t, found.Key), found.Checksum))

	out, err = runCLI(t, env.deps, verifyWord, optConfig, env.config)
	require.NoError(t, err, out)
	assert.Contains(t, out, "ok: every checked object matches")
}

const e2eSegmentFlags = `uses-segment:
  variations:
    "on": true
    "off": false
  targeting:
    - name: beta rule
      query: targetingKey in segment("ghost")
      variation: "on"
  defaultRule:
    variation: "off"
`

// writeSegmentTree adds a flag file that uses the segment "ghost" and a flag set that lists the segments object
// "people", which does not define it.
func (e *e2eEnv) writeSegmentTree(t *testing.T) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Join(e.dir, "segments"), 0o750))
	e.write(t, "flags/uses-segment.yaml", e2eSegmentFlags)
	e.write(t, "segments/people.yaml", e2eSegments)
	e.write(t, "flagsets/segment-sets.yaml", strings.Replace(
		e2eSegmentSets, "sources: [checkout]", "sources: [uses-segment]", 1))
}

func TestE2EPublishRefusesAnUndefinedSegment(t *testing.T) {
	t.Parallel()

	env := newE2E(t)
	env.writeSegmentTree(t)

	out, err := runCLI(t, env.deps, env.dir, optConfig, env.config)
	require.Error(t, err, out)
	assert.Equal(t, helper.ExitInvalid, codeOf(err), "the exit code of the other validation failures")
	assert.Contains(t, err.Error(), "flag set with-segments: flag uses-segment, rule 0: segment ghost: is not defined")
	assert.Zero(t, env.count.put.Load(), "nothing is written")
}

func TestE2EPublishRefusesANameDefinedTwice(t *testing.T) {
	t.Parallel()

	env := newE2E(t)
	env.writeSegmentTree(t)
	env.write(
		t,
		"segments/more.yaml",
		"apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: beta\n    members: []\n",
	)
	env.write(t, "flagsets/segment-sets.yaml", strings.Replace(
		e2eSegmentSets, "segmentSources: [people]", "segmentSources: [people, more]", 1))

	out, err := runCLI(t, env.deps, env.dir, optConfig, env.config)
	require.Error(t, err, out)
	assert.Equal(t, helper.ExitInvalid, codeOf(err))
	assert.Contains(t, err.Error(), "flag set with-segments: segment beta: defined twice")
}

func TestE2EVerifyReportsAnUndefinedSegment(t *testing.T) {
	t.Parallel()

	env := newE2E(t)
	env.writeSegmentTree(t)

	// the tree is published without the checks of the CLI, as an older tool could have done
	objects, err := publish.LoadSource(os.DirFS(env.dir), env.dir, publish.SourceLimits{})
	require.NoError(t, err)

	pub, err := s3publish.New(env.client, &s3publish.Config{
		Bucket: env.bucket, Channel: e2eChannelName, Connection: "control", ManifestKey: manifestKey, Concurrency: 1,
	})
	require.NoError(t, err)

	_, err = pub.Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	out, err := runCLI(t, env.deps, verifyWord, optConfig, env.config)
	require.Error(t, err, out)
	assert.Contains(t, out+err.Error(), "segment ghost: is not defined")
}

const (
	e2ePublicSets = "apiVersion: semaphore/v1\nkind: FlagSet\nflagSets:\n" +
		"  - name: public-one\n    sources: [public-one]\n  - name: public-two\n    sources: [public-two]\n"
	e2ePublicFlags = "shared:\n  variations: {\"on\": true, \"off\": false}\n  defaultRule: {variation: \"on\"}\n"
	e2ePublicFixed = "other:\n  variations: {\"on\": true, \"off\": false}\n  defaultRule: {variation: \"on\"}\n"
)

// servedPublicFlags feeds the published generation to the engine of the relay and returns the flag keys that a request
// without an API key is served.
func (e *e2eEnv) servedPublicFlags(t *testing.T) []string {
	t.Helper()

	manifest := e.readManifest(t)
	content := &flagruntime.Content{Generation: manifest.Generation, Objects: map[string]flagruntime.Object{}}

	for i := range manifest.Objects {
		entry := &manifest.Objects[i]
		content.Objects[entry.Name] = flagruntime.Object{
			Kind: entry.Kind, Format: entry.Format, Bytes: e.get(t, entry.Key),
		}
	}

	rounds := make(chan flagruntime.RoundResult, 1)
	holder := flagruntime.NewHolder()
	svc := flagdef.NewService()
	t.Cleanup(svc.Close)

	engine := flagruntime.NewEngine(slog.New(slog.DiscardHandler), svc, holder,
		flagruntime.WithHooks(flagruntime.Hooks{AfterRound: func(res flagruntime.RoundResult) { rounds <- res }}))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		engine.Run(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	engine.Submit(content)

	select {
	case res := <-rounds:
		require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)
	case <-time.After(e2eBound):
		require.FailNow(t, "the relay did not apply the generation")
	}

	set, route := holder.Load().Route("")
	require.Equal(t, flagruntime.RouteFlagSet, route)

	keys := make([]string, 0, len(set.Flags))
	for key := range set.Flags {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

const e2eBound = 10 * time.Second

// Two flag sets without API keys that define the same flag key are refused before anything is written, the way the
// relay would reject the generation; once the key is made unique the tree is published, and the relay serves both
// flags to a request without a key.
func TestE2EPublishRefusesAFlagKeySharedByKeylessFlagSets(t *testing.T) {
	t.Parallel()

	env := newE2E(t)
	env.write(t, "flags/public-one.yaml", e2ePublicFlags)
	env.write(t, "flags/public-two.yaml", e2ePublicFlags)
	env.write(t, "flagsets/public.yaml", e2ePublicSets)

	out, err := runCLI(t, env.deps, env.dir, optConfig, env.config)
	require.Error(t, err, out)
	assert.Equal(t, helper.ExitInvalid, codeOf(err))
	assert.Contains(t, err.Error(),
		"flag shared: defined in the keyless flag sets public-one (flag object public-one) and public-two")
	assert.Zero(t, env.count.put.Load(), "nothing is written")

	env.write(t, "flags/public-two.yaml", e2ePublicFixed)

	out, err = runCLI(t, env.deps, env.dir, optConfig, env.config)
	require.NoError(t, err, out)
	assert.Equal(t, []string{"other", "shared"}, env.servedPublicFlags(t))

	out, err = runCLI(t, env.deps, verifyWord, optConfig, env.config)
	require.NoError(t, err, out)
}
