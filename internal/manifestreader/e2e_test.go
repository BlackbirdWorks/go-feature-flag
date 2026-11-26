//go:build docker

package manifestreader_test

import (
	"net/http"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

// wireRequest is one HTTP request that reached the emulator.
type wireRequest struct {
	Method string
	Path   string
}

// wireCounter counts the HTTP requests that reach the emulator.
type wireCounter struct {
	seen []wireRequest
	mu   sync.Mutex
}

func (w *wireCounter) RoundTrip(req *http.Request) (*http.Response, error) {
	w.mu.Lock()
	w.seen = append(w.seen, wireRequest{Method: req.Method, Path: req.URL.Path})
	w.mu.Unlock()

	return http.DefaultTransport.RoundTrip(req)
}

func (w *wireCounter) take() []wireRequest {
	w.mu.Lock()
	defer w.mu.Unlock()

	out := w.seen
	w.seen = nil

	return out
}

// observed is a connection.RequestObserver that keeps the observations.
type observed struct {
	obs []observation
	mu  sync.Mutex
}

type observation struct{ Connection, Operation, Result string }

func (o *observed) Observe(conn, operation, result string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.obs = append(o.obs, observation{conn, operation, result})
}

func (o *observed) take() []observation {
	o.mu.Lock()
	defer o.mu.Unlock()

	out := o.obs
	o.obs = nil

	return out
}

// e2eRig is a reader on an isolated bucket of the emulator.
type e2eRig struct {
	*rig

	srv    *gopherstack.Server
	wire   *wireCounter
	obs    *observed
	bucket string
}

// publishE2E writes the objects, then the manifest, with the marshalers of the controlplane package.
func (e *e2eRig) publishE2E(t *testing.T, gen uint64, objs ...obj) {
	t.Helper()

	for _, o := range objs {
		e.srv.PutObject(t, e.bucket, keyOf(t, o), []byte(o.content))
	}

	e.srv.PutObject(t, e.bucket, manifestKey, mustMarshal(t, manifestOf(t, gen, objs...)))
}

func (e *e2eRig) objectPath(t *testing.T, item obj) string {
	t.Helper()

	return "/" + e.bucket + "/" + keyOf(t, item)
}

func (e *e2eRig) manifestPath() string { return "/" + e.bucket + "/" + manifestKey }

func e2eConfig() *manifestreader.Config {
	return &manifestreader.Config{DisableJitter: true, FetchConcurrency: 1}
}

func TestE2E_PollingCostsOneConditionalGet(t *testing.T) {
	t.Parallel()

	rdr := startE2EWithGeneration1(t)

	const polls = 10

	for range polls {
		rdr.round()
	}

	wire, obs := rdr.wire.take(), rdr.obs.take()

	// N polls cost exactly N requests, and both counters agree
	require.Len(t, wire, polls)
	require.Len(t, obs, polls)
	assert.Len(t, obs, len(wire), "the observer and the wire saw the same number of requests")

	for i := range polls {
		assert.Equal(t, wireRequest{Method: http.MethodGet, Path: rdr.manifestPath()}, wire[i])
		assert.Equal(t, observation{connControl, connection.OperationGet, connection.ResultNotModified}, obs[i])
	}

	assert.Nil(t, rdr.snapshot(), "nothing was delivered")
	assert.Equal(t, uint64(polls), rdr.reader.Status().NotModified)
}

// startE2EWithGeneration1 publishes generation 1 of three objects, starts the reader, and checks the cold start.
func startE2EWithGeneration1(t *testing.T) *e2eRig {
	t.Helper()

	rdr := startE2ENoWait(t)
	rdr.settle()

	snap := rdr.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, uint64(1), snap.Generation)

	for _, o := range threeObjects() {
		assert.Equal(t, okState(t, o), snap.Objects[o.name])
	}

	// cold start: the manifest and one GET per object, nothing else
	wire, obs := rdr.wire.take(), rdr.obs.take()
	assert.Len(t, wire, 1+len(threeObjects()))
	assert.Len(t, obs, 1+len(threeObjects()))

	for _, req := range wire {
		assert.Equal(t, http.MethodGet, req.Method, "no HEAD, no LIST")
	}

	return rdr
}

// startE2ENoWait publishes generation 1 and starts the reader without waiting for its first round.
func startE2ENoWait(t *testing.T) *e2eRig {
	t.Helper()

	srv := gopherstack.Connect(t)
	bucket := srv.CreateBucket(t, "mr")
	wire, obs := &wireCounter{}, &observed{}

	fac := connection.New(connection.WithRoundTripper(wire), connection.WithObserver(obs))
	require.NoError(t, fac.Set(connControl, &connection.S3Spec{
		Endpoint: srv.Endpoint, Region: gopherstack.Region, Bucket: bucket, PathStyle: true,
		Credentials: connection.Credentials{
			Type: connection.CredentialsStatic, AccessKeyID: connection.NewSecret(gopherstack.AccessKey),
			SecretAccessKey: connection.NewSecret(gopherstack.SecretKey),
		},
	}))

	rdr := &e2eRig{srv: srv, bucket: bucket, wire: wire, obs: obs}
	rdr.publishE2E(t, 1, threeObjects()...)
	rdr.rig = launchRig(t, manifestreader.NewS3Store(fac, connControl, bucket), e2eConfig())

	return rdr
}

func TestE2E_OneChangedObjectCostsTwoRequests(t *testing.T) {
	t.Parallel()

	rdr := startE2EWithGeneration1(t)

	alpha2 := obj{name: nameAlpha, content: contentAlphaChanged}
	rdr.publishE2E(t, 2, alpha2, threeObjects()[1], threeObjects()[2])

	rdr.round()

	wire, obs := rdr.wire.take(), rdr.obs.take()
	assert.Equal(t, []wireRequest{
		{Method: http.MethodGet, Path: rdr.manifestPath()},
		{Method: http.MethodGet, Path: rdr.objectPath(t, alpha2)},
	}, wire)
	assert.Equal(t, []observation{
		{connControl, connection.OperationGet, connection.ResultOK},
		{connControl, connection.OperationGet, connection.ResultOK},
	}, obs)

	snap := rdr.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, uint64(2), snap.Generation)
	assert.Equal(t, okState(t, alpha2), snap.Objects[nameAlpha])
	assert.Equal(t, okState(t, threeObjects()[1]), snap.Objects[nameBeta])

	// and quiet again
	rdr.round()
	assert.Len(t, rdr.wire.take(), 1)
	assert.Equal(t, []observation{{connControl, connection.OperationGet, connection.ResultNotModified}}, rdr.obs.take())
}

func TestE2E_CorruptObjectIsStaleAndTheOthersUpdate(t *testing.T) {
	t.Parallel()

	rdr := startE2EWithGeneration1(t)

	alpha2 := obj{name: nameAlpha, content: contentAlphaSecond}
	beta2 := obj{name: nameBeta, content: `{"beta":"second"}`}

	// the publisher's object for alpha is overwritten with other bytes after the manifest listed its checksum
	rdr.publishE2E(t, 2, alpha2, beta2, threeObjects()[2])
	rdr.srv.PutObject(t, rdr.bucket, keyOf(t, alpha2), []byte(`{"alpha":"SECOND"}`))

	rdr.round()
	rdr.wire.take()

	snap := rdr.snapshot()
	require.NotNil(t, snap)

	stale := okState(t, threeObjects()[0])
	stale.State = manifestreader.StateStale
	stale.Reason = manifestreader.ReasonChecksumMismatch

	assert.Equal(t, stale, snap.Objects[nameAlpha], "the last good content, its own checksum, the reason")
	assert.Equal(t, okState(t, beta2), snap.Objects[nameBeta], "the other objects of the generation are updated")

	assert.Equal(t, []manifestreader.ObjectFailure{{
		Name: nameAlpha, State: manifestreader.StateStale, Reason: manifestreader.ReasonChecksumMismatch,
		Checksum: controlplane.ChecksumOf([]byte(alpha2.content)).Value, Retryable: true,
	}}, rdr.reader.Status().Failures)

	// the publisher repairs the object: the retry costs the manifest GET (304) and the one object GET
	rdr.srv.PutObject(t, rdr.bucket, keyOf(t, alpha2), []byte(alpha2.content))
	rdr.round()

	assert.Equal(t, []wireRequest{
		{Method: http.MethodGet, Path: rdr.manifestPath()},
		{Method: http.MethodGet, Path: rdr.objectPath(t, alpha2)},
	}, rdr.wire.take())

	snap = rdr.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, uint64(2), snap.Generation)
	assert.Equal(t, okState(t, alpha2), snap.Objects[nameAlpha])
}

func TestE2E_DeletedManifestKeepsThePreviousSnapshot(t *testing.T) {
	t.Parallel()

	rdr := startE2EWithGeneration1(t)

	_, err := rdr.srv.S3Client(t).DeleteObject(t.Context(), &s3.DeleteObjectInput{
		Bucket: aws.String(rdr.bucket), Key: aws.String(manifestKey),
	})
	require.NoError(t, err)

	rdr.round()

	assert.Equal(t, []wireRequest{{Method: http.MethodGet, Path: rdr.manifestPath()}}, rdr.wire.take())
	assert.Equal(t, []observation{{connControl, connection.OperationGet, connection.ResultNotFound}}, rdr.obs.take())
	assert.Nil(t, rdr.snapshot(), "the consumer keeps what it has")

	status := rdr.reader.Status()
	assert.Equal(t, manifestreader.PollNotFound, status.Poll.Result)
	assert.Equal(t, accepted(1, 3), status.Manifest)
	assert.Equal(t, manifestreader.Counts{OK: 3}, status.Counts)

	// the manifest comes back with a new generation and is picked up
	alpha2 := obj{name: nameAlpha, content: `{"alpha":"back"}`}
	rdr.publishE2E(t, 2, alpha2, threeObjects()[1], threeObjects()[2])
	rdr.round()

	snap := rdr.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, uint64(2), snap.Generation)
}
