//go:build docker

package manifestreader_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/storetest"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

// faultTransport answers the faults of the suite itself (the emulator has no permissions and no failures) and
// holds the requests of a key that hangs; every other request goes to the emulator.
type faultTransport struct {
	faults    map[string]storetest.Fault
	hangs     map[string]*storetest.Hang
	bodyHangs map[string]*storetest.Hang
	mu        sync.Mutex
}

// bodyPassed is the part of a body a stalled read still gets.
const bodyPassed = 1024

// stallingBody lets the first bytes through and then holds the reader until its context ends.
type stallingBody struct {
	io.ReadCloser

	ctx    context.Context //nolint:containedctx // the context of the request the body belongs to
	hang   *storetest.Hang
	passed int
}

func (b *stallingBody) Read(buf []byte) (int, error) {
	if b.passed >= bodyPassed {
		err := b.hang.Block(b.ctx)
		if err != nil {
			return 0, err
		}

		return b.ReadCloser.Read(buf)
	}

	n, err := b.ReadCloser.Read(buf[:min(len(buf), bodyPassed-b.passed)])
	b.passed += n

	return n, err
}

func (f *faultTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	fault, hang, bodyHang := f.faults[req.URL.Path], f.hangs[req.URL.Path], f.bodyHangs[req.URL.Path]
	f.mu.Unlock()

	if hang != nil {
		err := hang.Block(req.Context())
		if err != nil {
			return nil, err
		}
	}

	switch fault {
	case storetest.FaultDenied:
		return xmlAnswer(req, http.StatusForbidden, deniedXML), nil
	case storetest.FaultTransient:
		return xmlAnswer(req, http.StatusInternalServerError, internalXML), nil
	case storetest.FaultThrottled:
		return xmlAnswer(req, http.StatusServiceUnavailable, slowDownXML), nil
	case storetest.FaultNone:
	}

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil || bodyHang == nil || resp.StatusCode != http.StatusOK {
		return resp, err
	}

	resp.Body = &stallingBody{ReadCloser: resp.Body, ctx: req.Context(), hang: bodyHang}

	return resp, nil
}

func xmlAnswer(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Status: strconv.Itoa(status) + " " + http.StatusText(status),
		Header:        http.Header{"Content-Type": {"application/xml"}, "Content-Length": {strconv.Itoa(len(body))}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)), Request: req,
	}
}

// s3Backend arranges objects in a bucket of the emulator.
type s3Backend struct {
	ctxTB     testing.TB
	client    *s3.Client
	transport *faultTransport
	bucket    string
}

var _ storetest.Backend = (*s3Backend)(nil)

func (b *s3Backend) Put(key string, content []byte) error {
	_, err := b.client.PutObject(b.ctxTB.Context(), &s3.PutObjectInput{
		Bucket: aws.String(b.bucket), Key: aws.String(key), Body: bytes.NewReader(content),
	})

	return err
}

func (b *s3Backend) Overwrite(key string, content []byte) error { return b.Put(key, content) }

func (b *s3Backend) Delete(key string) error {
	_, err := b.client.DeleteObject(b.ctxTB.Context(), &s3.DeleteObjectInput{
		Bucket: aws.String(b.bucket), Key: aws.String(key),
	})

	return err
}

func (b *s3Backend) Fail(key string, fault storetest.Fault) error {
	b.transport.mu.Lock()
	defer b.transport.mu.Unlock()

	b.transport.faults[b.path(key)] = fault

	return nil
}

func (b *s3Backend) HangBody(key string) (*storetest.Hang, error) {
	hang := storetest.NewHang()

	b.transport.mu.Lock()
	b.transport.bodyHangs[b.path(key)] = hang
	b.transport.mu.Unlock()

	return hang, nil
}

func (b *s3Backend) PutVersion(key string, content []byte) (string, error) {
	out, err := b.client.PutObject(b.ctxTB.Context(), &s3.PutObjectInput{
		Bucket: aws.String(b.bucket), Key: aws.String(key), Body: bytes.NewReader(content),
	})
	if err != nil {
		return "", err
	}

	return aws.ToString(out.VersionId), nil
}

func (b *s3Backend) Hang(key string) (*storetest.Hang, error) {
	hang := storetest.NewHang()

	b.transport.mu.Lock()
	b.transport.hangs[b.path(key)] = hang
	b.transport.mu.Unlock()

	return hang, nil
}

// path is the URL path of a key, which names the key in the transport.
func (b *s3Backend) path(key string) string { return "/" + b.bucket + "/" + key }

// s3Waivers is what the S3 store does not offer.
func s3Waivers() storetest.Waivers {
	return storetest.NewWaivers().
		With(storetest.CapKeyValidation, "S3 keys are opaque strings; the reader validates them")
}

func s3Fixture(t *testing.T) storetest.Fixture {
	t.Helper()

	srv := gopherstack.Connect(t)
	bucket := srv.CreateBucket(t, "conf")
	transport := &faultTransport{
		faults:    map[string]storetest.Fault{},
		hangs:     map[string]*storetest.Hang{},
		bodyHangs: map[string]*storetest.Hang{},
	}

	fac := connection.New(connection.WithRoundTripper(transport))
	require.NoError(t, fac.Set(connControl, &connection.S3Spec{
		Endpoint: srv.Endpoint, Region: gopherstack.Region, Bucket: bucket, PathStyle: true,
		Credentials: connection.Credentials{
			Type: connection.CredentialsStatic, AccessKeyID: connection.NewSecret(gopherstack.AccessKey),
			SecretAccessKey: connection.NewSecret(gopherstack.SecretKey),
		},
		// one attempt: a fault answers every attempt, and the SDK would wait between them
		Retry: connection.Retry{MaxAttempts: 1, MaxBackoff: time.Millisecond},
	}))

	client := srv.S3Client(t)
	_, err := client.PutBucketVersioning(t.Context(), &s3.PutBucketVersioningInput{
		Bucket:                  aws.String(bucket),
		VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled},
	})
	require.NoError(t, err)

	return storetest.Fixture{
		Store:       manifestreader.NewS3Store(fac, connControl, bucket),
		Backend:     &s3Backend{client: client, transport: transport, bucket: bucket, ctxTB: t},
		Unsupported: s3Waivers(),
	}
}

func TestConformance_S3Store(t *testing.T) {
	t.Parallel()

	report := storetest.Run(t, "s3", s3Waivers(), s3Fixture)

	assert.Equal(t, map[string]string{
		"unclean-keys-are-refused": "key-validation: S3 keys are opaque strings; the reader validates them",
	}, report.Unsupported)
	assert.ElementsMatch(t, ranCases(report.Unsupported), report.Ran)
}
