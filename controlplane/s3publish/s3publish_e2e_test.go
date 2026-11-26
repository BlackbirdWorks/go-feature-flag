//go:build docker

package s3publish_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

// countingTransport counts the requests by method that reach the emulator.
type countingTransport struct {
	next    http.RoundTripper
	methods map[string]*atomic.Int32
	mu      sync.Mutex
}

func newCountingTransport() *countingTransport {
	return &countingTransport{next: http.DefaultTransport, methods: map[string]*atomic.Int32{}}
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.mu.Lock()

	counter, ok := c.methods[req.Method]
	if !ok {
		counter = &atomic.Int32{}
		c.methods[req.Method] = counter
	}

	c.mu.Unlock()
	counter.Add(1)

	return c.next.RoundTrip(req)
}

func (c *countingTransport) count(method string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if counter, ok := c.methods[method]; ok {
		return int(counter.Load())
	}

	return 0
}

func (c *countingTransport) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.methods = map[string]*atomic.Int32{}
}

// e2eEnv is a bucket on the emulator and a client that counts its requests.
type e2eEnv struct {
	client    *s3.Client
	transport *countingTransport
	bucket    string
}

func newE2E(t *testing.T) *e2eEnv {
	t.Helper()

	srv := gopherstack.Connect(t)
	transport := newCountingTransport()

	cfg := srv.MustAwsConfig(t)
	cfg.HTTPClient = &http.Client{Transport: transport}

	env := &e2eEnv{client: s3.NewFromConfig(cfg), transport: transport, bucket: gopherstack.UniqueName(t, "pub")}

	_, err := env.client.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(env.bucket)})
	require.NoError(t, err)

	transport.reset()

	return env
}

func (e *e2eEnv) publisher(t *testing.T) *s3publish.Publisher {
	t.Helper()

	pub, err := s3publish.New(e.client, &s3publish.Config{
		Bucket: e.bucket, Channel: testChannel, Connection: testConnection,
	})
	require.NoError(t, err)

	return pub
}

func (e *e2eEnv) get(t *testing.T, key string) ([]byte, string) {
	t.Helper()

	out, err := e.client.GetObject(t.Context(), &s3.GetObjectInput{Bucket: aws.String(e.bucket), Key: aws.String(key)})
	require.NoError(t, err)

	defer func() { _ = out.Body.Close() }()

	var buf bytes.Buffer

	_, err = buf.ReadFrom(out.Body)
	require.NoError(t, err)

	return buf.Bytes(), aws.ToString(out.ETag)
}

func (e *e2eEnv) put(t *testing.T, key string, body []byte) {
	t.Helper()

	_, err := e.client.PutObject(t.Context(), &s3.PutObjectInput{
		Bucket: aws.String(e.bucket), Key: aws.String(key), Body: bytes.NewReader(body),
	})
	require.NoError(t, err)
}

// assertBucketState reads the manifest with the controlplane package and checks every object it lists.
func (e *e2eEnv) assertBucketState(t *testing.T, wantGeneration uint64, wantBodies ...[]byte) {
	t.Helper()

	raw, _ := e.get(t, manifestKey)

	manifest, _, err := controlplane.UnmarshalManifest(raw)
	require.NoError(t, err)
	require.NoError(t, controlplane.ValidateManifest(manifest))
	assert.Equal(t, wantGeneration, manifest.Generation)
	assert.Equal(t, testChannel, manifest.Channel)
	require.Len(t, manifest.Objects, len(wantBodies))

	for i := range manifest.Objects {
		obj := &manifest.Objects[i]
		body, _ := e.get(t, obj.Key)
		assert.Equal(t, wantBodies[i], body, obj.Name)
		require.NoError(t, controlplane.VerifyChecksum(body, obj.Checksum), obj.Name)
		assert.Equal(t, obj.Size, int64(len(body)), obj.Name)
		assert.Equal(t, testConnection, obj.Connection)
	}
}

func TestE2EEmulatorConditionalWrites(t *testing.T) {
	t.Parallel()

	env := newE2E(t)
	put := func(in *s3.PutObjectInput) error {
		in.Bucket = aws.String(env.bucket)
		in.Body = bytes.NewReader([]byte("body"))
		_, err := env.client.PutObject(t.Context(), in)

		return err
	}
	status := func(err error) (int, string) {
		var withStatus interface{ HTTPStatusCode() int }

		var ae smithy.APIError

		require.ErrorAs(t, err, &withStatus)
		require.ErrorAs(t, err, &ae)

		return withStatus.HTTPStatusCode(), ae.ErrorCode()
	}

	// If-None-Match: * creates once.
	require.NoError(t, put(&s3.PutObjectInput{Key: aws.String("k"), IfNoneMatch: aws.String("*")}))

	code, apiCode := status(put(&s3.PutObjectInput{Key: aws.String("k"), IfNoneMatch: aws.String("*")}))
	assert.Equal(t, http.StatusPreconditionFailed, code)
	assert.Equal(t, "PreconditionFailed", apiCode)

	// If-Match with a stale ETag fails, with the current one succeeds.
	_, etag := env.get(t, "k")
	code, apiCode = status(put(&s3.PutObjectInput{Key: aws.String("k"), IfMatch: aws.String(`"stale"`)}))
	assert.Equal(t, http.StatusPreconditionFailed, code)
	assert.Equal(t, "PreconditionFailed", apiCode)
	require.NoError(t, put(&s3.PutObjectInput{Key: aws.String("k"), IfMatch: aws.String(etag)}))

	// If-Match on a missing key is 404 NoSuchKey.
	code, apiCode = status(put(&s3.PutObjectInput{Key: aws.String("absent"), IfMatch: aws.String(etag)}))
	assert.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "NoSuchKey", apiCode)

	// A wrong ChecksumSHA256 is refused: the publisher relies on it to verify uploads.
	code, apiCode = status(
		put(
			&s3.PutObjectInput{
				Key:            aws.String("bad"),
				ChecksumSHA256: aws.String("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="),
			},
		),
	)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "BadDigest", apiCode)

	// HEAD reports the sha256 given at upload (the publisher compares it).
	raw := sha256.Sum256([]byte("body"))
	wantSum := base64.StdEncoding.EncodeToString(raw[:])

	require.NoError(t, put(&s3.PutObjectInput{Key: aws.String("sum"), ChecksumSHA256: aws.String(wantSum)}))

	head, err := env.client.HeadObject(t.Context(), &s3.HeadObjectInput{
		Bucket: aws.String(env.bucket), Key: aws.String("sum"), ChecksumMode: types.ChecksumModeEnabled,
	})
	require.NoError(t, err)
	assert.Equal(t, wantSum, aws.ToString(head.ChecksumSHA256))
	assert.Equal(t, int64(4), aws.ToInt64(head.ContentLength))
}

func TestE2EPublishFirstRepublishAndNoChange(t *testing.T) {
	t.Parallel()

	env := newE2E(t)
	pub := env.publisher(t)
	objects := standardObjects(t, "a")

	res, err := pub.Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	assert.Equal(t, uint64(1), res.Generation)
	assert.Equal(t, 2, res.Uploaded)
	assert.Equal(t, 3, env.transport.count(http.MethodPut), "two objects and the manifest")
	assert.Equal(t, s3publish.Requests{Get: 1, Head: 2, Put: 3}, res.Requests)
	env.assertBucketState(t, 1, objects[0].Body, objects[1].Body)

	_, etag := env.get(t, manifestKey)
	assert.Equal(t, etag, res.ManifestETag)

	// One changed object: one object PUT and one manifest PUT.
	env.transport.reset()

	changed := []s3publish.Object{standardObjects(t, "b")[0], objects[1]}

	res, err = pub.Publish(t.Context(), s3publish.Request{Objects: changed})
	require.NoError(t, err)

	assert.Equal(t, uint64(2), res.Generation)
	assert.Equal(t, 1, res.Uploaded)
	assert.Equal(t, 1, res.Present, "the unchanged object is found, not uploaded again")
	assert.Equal(t, 2, env.transport.count(http.MethodPut), "the changed object and the manifest")
	env.assertBucketState(t, 2, changed[0].Body, changed[1].Body)

	// Nothing changed: no write at all.
	env.transport.reset()

	res, err = pub.Publish(t.Context(), s3publish.Request{Objects: changed})
	require.NoError(t, err)

	assert.False(t, res.Written)
	assert.Equal(t, uint64(2), res.Generation)
	assert.Zero(t, env.transport.count(http.MethodPut))
	assert.Equal(t, 1, env.transport.count(http.MethodGet))

	// Dry run of a change: reads only.
	env.transport.reset()

	res, err = pub.Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "c"), DryRun: true})
	require.NoError(t, err)

	assert.True(t, res.Changed)
	assert.False(t, res.Written)
	assert.Zero(t, env.transport.count(http.MethodPut))
	env.assertBucketState(t, 2, changed[0].Body, changed[1].Body)
}

func TestE2ETwoPublishersExactlyOneWins(t *testing.T) {
	t.Parallel()

	env := newE2E(t)
	gated := &gatedAPI{API: env.client}
	cfg := &s3publish.Config{Bucket: env.bucket, Channel: testChannel, Connection: testConnection}

	pubA, err := s3publish.New(gated, cfg)
	require.NoError(t, err)

	pubB, err := s3publish.New(gated, cfg)
	require.NoError(t, err)

	var wins []uint64

	for round := range 3 {
		gated.setGate(newBarrier(2))

		errs := make([]error, 2)
		results := make([]*s3publish.Result, 2)

		docs := [][]s3publish.Object{
			standardObjects(t, "a"+string(rune('0'+round))), standardObjects(t, "b"+string(rune('0'+round))),
		}

		var wg sync.WaitGroup

		for i, pub := range []*s3publish.Publisher{pubA, pubB} {
			wg.Go(func() {
				results[i], errs[i] = pub.Publish(t.Context(), s3publish.Request{Objects: docs[i]})
			})
		}

		wg.Wait()

		won, lost := 0, 0

		for i := range errs {
			switch {
			case errs[i] == nil:
				won++

				wins = append(wins, results[i].Generation)
			case errors.Is(errs[i], s3publish.ErrConflict):
				lost++
			default:
				require.NoError(t, errs[i])
			}
		}

		require.Equal(t, 1, won, "round %d", round)
		require.Equal(t, 1, lost, "round %d", round)

		raw, _ := env.get(t, manifestKey)
		manifest, _, unmarshalErr := controlplane.UnmarshalManifest(raw)
		require.NoError(t, unmarshalErr)
		assert.Equal(t, uint64(round+1), manifest.Generation)

		for _, obj := range manifest.Objects {
			body, _ := env.get(t, obj.Key)
			require.NoError(t, controlplane.VerifyChecksum(body, obj.Checksum))
		}
	}

	assert.Equal(t, []uint64{1, 2, 3}, wins)

	rep, err := env.publisher(t).Verify(t.Context())
	require.NoError(t, err)
	assert.True(t, rep.OK(), "%v", rep.Problems)
}

func TestE2EManyPublishersLeaveAConsistentBucket(t *testing.T) {
	t.Parallel()

	env := newE2E(t)

	const publishers = 8

	var wg sync.WaitGroup

	wonAt := make([]uint64, publishers) // generation won by publisher i, 0 when it lost

	pubs := make([]*s3publish.Publisher, publishers)
	docs := make([][]s3publish.Object, publishers)

	for i := range publishers {
		pubs[i] = env.publisher(t)
		docs[i] = standardObjects(t, string(rune('a'+i)))
	}

	for i := range publishers {
		wg.Go(func() {
			res, err := pubs[i].Publish(t.Context(), s3publish.Request{Objects: docs[i]})
			if err != nil {
				assert.ErrorIs(t, err, s3publish.ErrConflict)

				return
			}

			wonAt[i] = res.Generation
		})
	}

	wg.Wait()

	var wins []uint64

	for _, generation := range wonAt {
		if generation != 0 {
			wins = append(wins, generation)
		}
	}

	require.NotEmpty(t, wins)
	assert.Len(t, uniq(wins), len(wins), "a generation is won once")

	rep, err := env.publisher(t).Verify(t.Context())
	require.NoError(t, err)
	assert.True(t, rep.OK(), "%v", rep.Problems)
}

func TestE2EVerifyFindsCorruptionAndMissingObjects(t *testing.T) {
	t.Parallel()

	env := newE2E(t)
	pub := env.publisher(t)
	objects := standardObjects(t, "a")

	_, err := pub.Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	rep, err := pub.Verify(t.Context())
	require.NoError(t, err)
	assert.True(t, rep.OK())
	assert.Equal(t, 2, rep.Checked)

	flagsKey := objectKey(t, objects[0].Body)
	setsKey := objectKey(t, objects[1].Body)

	corrupted := bytes.Clone(objects[0].Body)
	corrupted[0] ^= 0x01
	env.put(t, flagsKey, corrupted)

	rep, err = pub.Verify(t.Context())
	require.NoError(t, err)
	require.Len(t, rep.Problems, 1)
	assert.Equal(t, nameFlags, rep.Problems[0].Object)
	assert.Equal(t, s3publish.ProblemChecksumMismatch, rep.Problems[0].Code)
	assert.Equal(t, flagsKey, rep.Problems[0].Key)

	// Republishing the same content never overwrites the corrupted object: the manifest is unchanged, so
	// nothing is written, and Verify keeps reporting it.
	res, err := pub.Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)
	assert.False(t, res.Written)

	_, err = env.client.DeleteObject(
		t.Context(),
		&s3.DeleteObjectInput{Bucket: aws.String(env.bucket), Key: aws.String(setsKey)},
	)
	require.NoError(t, err)

	rep, err = pub.Verify(t.Context())
	require.NoError(t, err)

	codes := map[string]string{}
	for _, pr := range rep.Problems {
		codes[pr.Object] = pr.Code
	}

	assert.Equal(t, map[string]string{
		nameFlags: s3publish.ProblemChecksumMismatch, nameSets: s3publish.ProblemMissing,
	}, codes)
}

func TestE2EPrefixAndJSONManifest(t *testing.T) {
	t.Parallel()

	env := newE2E(t)

	pub, err := s3publish.New(env.client, &s3publish.Config{
		Bucket: env.bucket, Prefix: "tenant-a", Channel: testChannel, Connection: testConnection,
		ManifestKey: "tenant-a/manifests/stable.json",
	})
	require.NoError(t, err)

	objects := standardObjects(t, "a")

	_, err = pub.Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	raw, _ := env.get(t, "tenant-a/manifests/stable.json")

	manifest, _, err := controlplane.UnmarshalManifest(raw)
	require.NoError(t, err)

	for _, obj := range manifest.Objects {
		assert.Contains(t, obj.Key, "tenant-a/objects/sha256/")
	}

	rep, err := pub.Verify(t.Context())
	require.NoError(t, err)
	assert.True(t, rep.OK(), "%v", rep.Problems)
}
