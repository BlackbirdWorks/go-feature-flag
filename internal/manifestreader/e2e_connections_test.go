//go:build docker

package manifestreader_test

import (
	"bytes"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

const (
	connObjects   = "objects"
	keyIDControl  = "AKIDCONTROL"
	keyIDObjects  = "AKIDOBJECTS"
	contentOnCtrl = `{"beta":"on the control store"}`
	contentOnObj  = `{"alpha":"on the objects store"}`
)

var credentialPattern = regexp.MustCompile(`Credential=([^/]+)/`)

// signedRequest is one request that reached an emulator: where it went and who signed it.
type signedRequest struct {
	Host   string
	Method string
	Path   string
	KeyID  string
}

// signedWire records the host, path and access key id of every request.
type signedWire struct {
	seen []signedRequest
	mu   sync.Mutex
}

func (w *signedWire) RoundTrip(req *http.Request) (*http.Response, error) {
	keyID := ""
	if match := credentialPattern.FindStringSubmatch(req.Header.Get("Authorization")); match != nil {
		keyID = match[1]
	}

	w.mu.Lock()
	w.seen = append(w.seen, signedRequest{Host: req.URL.Host, Method: req.Method, Path: req.URL.Path, KeyID: keyID})
	w.mu.Unlock()

	return http.DefaultTransport.RoundTrip(req)
}

func (w *signedWire) take() []signedRequest {
	w.mu.Lock()
	defer w.mu.Unlock()

	out := w.seen
	w.seen = nil

	return out
}

func s3SpecOn(endpoint, bucket, keyID string) *connection.S3Spec {
	return &connection.S3Spec{
		Endpoint: endpoint, Region: gopherstack.Region, Bucket: bucket, PathStyle: true,
		Credentials: connection.Credentials{
			Type: connection.CredentialsStatic, AccessKeyID: connection.NewSecret(keyID),
			SecretAccessKey: connection.NewSecret(gopherstack.SecretKey),
		},
	}
}

// TestE2E_ObjectsOnASecondConnectionWithTheSameBucketName reads the manifest from one connection and one of the
// two objects from a second connection (another emulator, other credentials) whose bucket has the same name.
// Each store also holds, under the key of the object that lives on the other store, different bytes of the same
// length: an object fetched from the wrong store passes the size check and fails its checksum.
func TestE2E_ObjectsOnASecondConnectionWithTheSameBucketName(t *testing.T) {
	t.Parallel()

	srvControl := gopherstack.Connect(t)

	srvObjects, err := gopherstack.StartIsolated(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, srvObjects.Terminate()) })

	require.NotEqual(t, srvControl.Endpoint, srvObjects.Endpoint, "two distinct emulators")

	// the same bucket name on both emulators
	bucket := srvControl.CreateBucket(t, "mr")
	_, err = srvObjects.S3Client(t).CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)

	alpha := obj{name: nameAlpha, content: contentOnObj} // lives on the objects connection
	beta := obj{name: nameBeta, content: contentOnCtrl}  // lives on the control connection
	manifest := manifestOf(t, 1, alpha, beta)
	manifest.Objects[0].Connection = connObjects

	srvObjects.PutObject(t, bucket, keyOf(t, alpha), []byte(alpha.content))
	srvObjects.PutObject(t, bucket, keyOf(t, beta), decoyOf(beta))
	srvControl.PutObject(t, bucket, keyOf(t, alpha), decoyOf(alpha))
	srvControl.PutObject(t, bucket, keyOf(t, beta), []byte(beta.content))
	srvControl.PutObject(t, bucket, manifestKey, mustMarshal(t, manifest))

	wire, obs := &signedWire{}, &observed{}
	fac := connection.New(connection.WithRoundTripper(wire), connection.WithObserver(obs))
	require.NoError(t, fac.Replace(map[string]connection.Spec{
		connControl: s3SpecOn(srvControl.Endpoint, bucket, keyIDControl),
		connObjects: s3SpecOn(srvObjects.Endpoint, bucket, keyIDObjects),
	}))

	rdr := launchRig(
		t,
		manifestreader.NewS3Store(fac, connControl, bucket),
		e2eConfig(),
		manifestreader.WithObjectStores(map[string]manifestreader.ObjectStore{
			connObjects: manifestreader.NewS3Store(fac, connObjects, bucket),
		}),
	)
	rdr.settle()

	snap := rdr.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, uint64(1), snap.Generation)
	assert.Equal(t, okState(t, alpha), snap.Objects[nameAlpha], "from the objects store, not the control decoy")
	assert.Equal(t, okState(t, beta), snap.Objects[nameBeta], "from the control store, not the objects decoy")
	assert.Empty(t, rdr.reader.Status().Failures, "no checksum mismatch: nothing was read from the wrong store")

	hostControl, hostObjects := hostOf(t, srvControl), hostOf(t, srvObjects)
	pathOf := func(o obj) string { return "/" + bucket + "/" + keyOf(t, o) }

	assert.Equal(t, []signedRequest{
		{Host: hostControl, Method: http.MethodGet, Path: "/" + bucket + "/" + manifestKey, KeyID: keyIDControl},
		{Host: hostObjects, Method: http.MethodGet, Path: pathOf(alpha), KeyID: keyIDObjects},
		{Host: hostControl, Method: http.MethodGet, Path: pathOf(beta), KeyID: keyIDControl},
	}, wire.take(), "one request each; a key id is only ever sent to the host of its own connection")
	assert.Equal(t, []observation{
		{connControl, connection.OperationGet, connection.ResultOK},
		{connObjects, connection.OperationGet, connection.ResultOK},
		{connControl, connection.OperationGet, connection.ResultOK},
	}, obs.take(), "the per-connection request counts")

	// a quiet round costs one conditional request, on the control connection only
	rdr.round()

	assert.Equal(t, []signedRequest{
		{Host: hostControl, Method: http.MethodGet, Path: "/" + bucket + "/" + manifestKey, KeyID: keyIDControl},
	}, wire.take())
	assert.Equal(t, []observation{{connControl, connection.OperationGet, connection.ResultNotModified}}, obs.take())
}

func hostOf(t *testing.T, srv *gopherstack.Server) string {
	t.Helper()

	parsed, err := url.Parse(srv.Endpoint)
	require.NoError(t, err)

	return parsed.Host
}

// decoyOf returns bytes of the length of item's content, so a decoy is rejected by its checksum and not by its size.
func decoyOf(item obj) []byte { return bytes.Repeat([]byte("x"), len(item.content)) }
