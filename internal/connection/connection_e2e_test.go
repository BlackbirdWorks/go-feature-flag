//go:build docker

package connection_test

import (
	"bytes"
	"io"
	"net"
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
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

const (
	connCond  = "cond"
	connTwoA  = "two-a"
	connTwoB  = "two-b"
	connCount = "count"
	keyTwoA   = "AKIDTWOA"
	keyTwoB   = "AKIDTWOB"
)

var credentialRE = regexp.MustCompile(`Credential=([^/]+)/`)

// countingTransport counts the HTTP requests that reach the emulator and records who signed them.
type countingTransport struct {
	next    http.RoundTripper
	hosts   []string
	keyIDs  []string
	methods []string
	mu      sync.Mutex
}

func newCounting() *countingTransport {
	return &countingTransport{next: http.DefaultTransport}
}

func (ct *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ct.mu.Lock()
	ct.hosts = append(ct.hosts, req.URL.Host)
	ct.methods = append(ct.methods, req.Method)

	keyID := ""
	if m := credentialRE.FindStringSubmatch(req.Header.Get("Authorization")); m != nil {
		keyID = m[1]
	}

	ct.keyIDs = append(ct.keyIDs, keyID)
	ct.mu.Unlock()

	return ct.next.RoundTrip(req)
}

func (ct *countingTransport) count() int {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	return len(ct.hosts)
}

// s3Conn is an S3 spec on the emulator at endpoint, with a static key.
func s3Conn(endpoint, bucket, keyID string) *connection.S3Spec {
	return &connection.S3Spec{
		Endpoint: endpoint,
		Region:   gopherstack.Region,
		Credentials: connection.Credentials{
			Type: connection.CredentialsStatic, AccessKeyID: connection.NewSecret(keyID),
			SecretAccessKey: connection.NewSecret(gopherstack.SecretKey),
		},
		Bucket:    bucket,
		PathStyle: true,
	}
}

// localhostEndpoint is the same emulator under another host name: a different endpoint for the factory. A
// remote emulator (GOPHERSTACK_ENDPOINT with another host) is returned unchanged, since localhost would not
// reach it.
func localhostEndpoint(t *testing.T, endpoint string) string {
	t.Helper()

	parsed, err := url.Parse(endpoint)
	require.NoError(t, err)

	if parsed.Hostname() != "127.0.0.1" {
		return endpoint
	}

	parsed.Host = net.JoinHostPort("localhost", parsed.Port())

	return parsed.String()
}

func mustHost(t *testing.T, endpoint string) string {
	t.Helper()

	parsed, err := url.Parse(endpoint)
	require.NoError(t, err)

	return parsed.Host
}

func createBucket(t *testing.T, c *s3.Client, bucket string) {
	t.Helper()

	_, err := c.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)
}

func putObject(t *testing.T, c *s3.Client, bucket, key, body string) string {
	t.Helper()

	out, err := c.PutObject(t.Context(), &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader([]byte(body)),
	})
	require.NoError(t, err)

	return aws.ToString(out.ETag)
}

func getObject(t *testing.T, c *s3.Client, bucket, key string) string {
	t.Helper()

	out, err := c.GetObject(t.Context(), &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	require.NoError(t, err)

	defer func() { _ = out.Body.Close() }()

	b, err := io.ReadAll(out.Body)
	require.NoError(t, err)

	return string(b)
}

func TestE2E_ParallelConnectionsOnOneEmulator(t *testing.T) {
	t.Parallel()

	srv := gopherstack.Connect(t)
	rec := &recorder{}
	fac := connection.New(connection.WithObserver(rec))

	const connections = 6

	names := make([]string, connections)
	buckets := make([]string, connections)
	specs := map[string]connection.Spec{}

	for i := range connections {
		names[i] = "par-" + string(rune('a'+i))
		buckets[i] = gopherstack.UniqueName(t, "par")

		endpoint := srv.Endpoint
		if i%2 == 1 {
			endpoint = localhostEndpoint(t, srv.Endpoint)
		}

		specs[names[i]] = s3Conn(endpoint, buckets[i], gopherstack.AccessKey)
	}

	require.NoError(t, fac.Replace(specs))

	for i := range connections {
		t.Run(names[i], func(t *testing.T) {
			t.Parallel()

			cli, err := fac.S3(t.Context(), names[i])
			require.NoError(t, err)

			createBucket(t, cli, buckets[i])
			putObject(t, cli, buckets[i], "k", "value-"+names[i])
			assert.Equal(t, "value-"+names[i], getObject(t, cli, buckets[i], "k"))
		})
	}

	t.Cleanup(func() {
		want := map[string]int{}
		for _, n := range names {
			want[n] = 1
		}

		assert.Equal(t, want, fac.Builds())
	})
}

func TestE2E_ConditionalGetIsNotModified(t *testing.T) {
	t.Parallel()

	srv := gopherstack.Connect(t)
	rec := &recorder{}
	transport := newCounting()
	fac := connection.New(connection.WithObserver(rec), connection.WithRoundTripper(transport))
	bucket := gopherstack.UniqueName(t, connCond)
	require.NoError(t, fac.Set(connCond, s3Conn(srv.Endpoint, bucket, gopherstack.AccessKey)))

	cli, err := fac.S3(t.Context(), connCond)
	require.NoError(t, err)

	createBucket(t, cli, bucket)
	etag := putObject(t, cli, bucket, "manifest", "v1")
	require.NotEmpty(t, etag)

	_, err = cli.GetObject(t.Context(), &s3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("manifest"), IfNoneMatch: aws.String(etag),
	})
	require.Error(t, err)
	assert.True(t, connection.IsNotModified(err))

	newETag := putObject(t, cli, bucket, "manifest", "v2")
	require.NotEqual(t, etag, newETag)

	out, err := cli.GetObject(t.Context(), &s3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("manifest"), IfNoneMatch: aws.String(etag),
	})
	require.NoError(t, err)
	require.NoError(t, out.Body.Close())

	want := []observation{
		{connCond, opOther, resOK},        // CreateBucket
		{connCond, opPut, resOK},          // v1
		{connCond, opGet, resNotModified}, // If-None-Match: current
		{connCond, opPut, resOK},          // v2
		{connCond, opGet, resOK},          // If-None-Match: stale
	}
	assert.Equal(t, want, rec.all())
	assert.Equal(t, len(want), transport.count())
}

func TestE2E_TwoConnectionsTwoCredentialsTwoBuckets(t *testing.T) {
	t.Parallel()

	srv := gopherstack.Connect(t)
	rec := &recorder{}
	transport := newCounting()
	fac := connection.New(connection.WithObserver(rec), connection.WithRoundTripper(transport))

	bucketA := gopherstack.UniqueName(t, "twoa")
	bucketB := gopherstack.UniqueName(t, "twob")
	endpointB := localhostEndpoint(t, srv.Endpoint)

	require.NoError(t, fac.Replace(map[string]connection.Spec{
		connTwoA: s3Conn(srv.Endpoint, bucketA, keyTwoA),
		connTwoB: s3Conn(endpointB, bucketB, keyTwoB),
	}))

	clientA, err := fac.S3(t.Context(), connTwoA)
	require.NoError(t, err)

	clientB, err := fac.S3(t.Context(), connTwoB)
	require.NoError(t, err)

	createBucket(t, clientA, bucketA)
	putObject(t, clientA, bucketA, "k", "from-a")
	assert.Equal(t, "from-a", getObject(t, clientA, bucketA, "k"))

	createBucket(t, clientB, bucketB)
	putObject(t, clientB, bucketB, "k", "from-b")
	assert.Equal(t, "from-b", getObject(t, clientB, bucketB, "k"))

	hostA := mustHost(t, srv.Endpoint)
	hostB := mustHost(t, endpointB)

	transport.mu.Lock()
	defer transport.mu.Unlock()

	wantKeys := []string{keyTwoA, keyTwoA, keyTwoA, keyTwoB, keyTwoB, keyTwoB}
	wantHosts := []string{hostA, hostA, hostA, hostB, hostB, hostB}

	assert.Equal(t, wantKeys, transport.keyIDs)
	assert.Equal(t, wantHosts, transport.hosts)

	wantConns := []string{connTwoA, connTwoA, connTwoA, connTwoB, connTwoB, connTwoB}
	gotConns := make([]string, 0, len(wantConns))

	for _, o := range rec.all() {
		gotConns = append(gotConns, o.Connection)
	}

	assert.Equal(t, wantConns, gotConns)
}

func TestE2E_RequestsEqualObservations(t *testing.T) {
	t.Parallel()

	srv := gopherstack.Connect(t)
	rec := &recorder{}
	transport := newCounting()
	fac := connection.New(connection.WithObserver(rec), connection.WithRoundTripper(transport))
	bucket := gopherstack.UniqueName(t, connCount)
	require.NoError(t, fac.Set(connCount, s3Conn(srv.Endpoint, bucket, gopherstack.AccessKey)))

	cli, err := fac.S3(t.Context(), connCount)
	require.NoError(t, err)

	createBucket(t, cli, bucket)
	etag := putObject(t, cli, bucket, "present", "x")
	assert.Equal(t, "x", getObject(t, cli, bucket, "present"))

	_, err = cli.GetObject(t.Context(), &s3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("present"), IfNoneMatch: aws.String(etag),
	})
	require.Error(t, err)
	assert.True(t, connection.IsNotModified(err))

	_, err = cli.GetObject(t.Context(), &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String("absent")})
	require.Error(t, err)

	_, err = cli.HeadObject(t.Context(), &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String("present")})
	require.NoError(t, err)

	_, err = cli.ListObjectsV2(t.Context(), &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	require.NoError(t, err)

	want := []observation{
		{connCount, opOther, resOK},
		{connCount, opPut, resOK},
		{connCount, opGet, resOK},
		{connCount, opGet, resNotModified},
		{connCount, opGet, resNotFound},
		{connCount, opHead, resOK},
		{connCount, opList, resOK},
	}
	assert.Equal(t, want, rec.all())
	assert.Equal(t, len(want), transport.count())
	assert.Equal(t, map[string]int{connCount: 1}, fac.Builds())
}
