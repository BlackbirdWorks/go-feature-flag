package connection_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

func TestObserver_GetObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		steps           []step
		ifNoneMatch     string
		wantObs         []observation
		wantRequests    int
		wantErr         bool
		wantNotModified bool
	}{
		{
			name:         "200 is ok",
			steps:        []step{{status: 200, body: valueHello}},
			wantObs:      []observation{{connC, opGet, resOK}},
			wantRequests: 1,
		},
		{
			name:            "304 on a conditional get is not_modified and distinguishable",
			steps:           []step{{status: 304}},
			ifNoneMatch:     `"etag-1"`,
			wantObs:         []observation{{connC, opGet, resNotModified}},
			wantRequests:    1,
			wantErr:         true,
			wantNotModified: true,
		},
		{
			name:         "404 is not_found and not retried",
			steps:        []step{{status: 404, body: noSuchKeyXML}},
			wantObs:      []observation{{connC, opGet, resNotFound}},
			wantRequests: 1,
			wantErr:      true,
		},
		{
			name:         "403 is client_error and not retried",
			steps:        []step{{status: 403, body: accessDeniedXML}},
			wantObs:      []observation{{connC, opGet, resClientError}},
			wantRequests: 1,
			wantErr:      true,
		},
		{
			name:         "503 SlowDown then 200 is two observations",
			steps:        []step{{status: 503, body: slowDownXML}, {status: 200, body: valueHello}},
			wantObs:      []observation{{connC, opGet, resThrottled}, {connC, opGet, resOK}},
			wantRequests: 2,
		},
		{
			name:         "500 then 200 is server_error then ok",
			steps:        []step{{status: 500, body: internalErrorXML}, {status: 200, body: valueHello}},
			wantObs:      []observation{{connC, opGet, resServerError}, {connC, opGet, resOK}},
			wantRequests: 2,
		},
		{
			name:         "429 Throttling then 200",
			steps:        []step{{status: 429, body: tooManyXML}, {status: 200, body: valueHello}},
			wantObs:      []observation{{connC, opGet, resThrottled}, {connC, opGet, resOK}},
			wantRequests: 2,
		},
		{
			name: "three attempts all throttled give three observations and an error",
			steps: []step{
				{status: 503, body: slowDownXML}, {status: 503, body: slowDownXML}, {status: 503, body: slowDownXML},
			},
			wantObs: []observation{
				{connC, opGet, resThrottled},
				{connC, opGet, resThrottled},
				{connC, opGet, resThrottled},
			},
			wantRequests: 3,
			wantErr:      true,
		},
		{
			name:         "transport error then 200",
			steps:        []step{{err: io.ErrUnexpectedEOF}, {status: 200, body: valueHello}},
			wantObs:      []observation{{connC, opGet, resError}, {connC, opGet, resOK}},
			wantRequests: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := newScripted(tt.steps...)
			rec := &recorder{}
			fac := connection.New(connection.WithRoundTripper(transport), connection.WithObserver(rec))
			require.NoError(t, fac.Set(connC, testS3()))

			client, err := fac.S3(t.Context(), connC)
			require.NoError(t, err)

			in := &s3.GetObjectInput{Bucket: aws.String("bucket"), Key: aws.String("k")}
			if tt.ifNoneMatch != "" {
				in.IfNoneMatch = aws.String(tt.ifNoneMatch)
			}

			out, err := client.GetObject(t.Context(), in)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)

				body, readErr := io.ReadAll(out.Body)
				require.NoError(t, readErr)
				assert.Equal(t, valueHello, string(body))
			}

			assert.Equal(t, tt.wantNotModified, connection.IsNotModified(err))
			assert.Equal(t, tt.wantObs, rec.all())
			assert.Equal(t, tt.wantRequests, transport.count())
		})
	}
}

func TestObserver_OperationLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(context.Context, *s3.Client) error
		want string
	}{
		{opGet, func(ctx context.Context, c *s3.Client) error {
			_, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("b"), Key: aws.String("k")})

			return err
		}, opGet},
		{opHead, func(ctx context.Context, c *s3.Client) error {
			_, err := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("b"), Key: aws.String("k")})

			return err
		}, opHead},
		{opList, func(ctx context.Context, c *s3.Client) error {
			_, err := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String("b")})

			return err
		}, opList},
		{opDelete, func(ctx context.Context, c *s3.Client) error {
			_, err := c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String("b"), Key: aws.String("k")})

			return err
		}, opDelete},
		{"create bucket is other", func(ctx context.Context, c *s3.Client) error {
			_, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("b")})

			return err
		}, opOther},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transport := newScripted(step{status: 200, body: "<Result/>"})
			rec := &recorder{}
			fac := connection.New(connection.WithRoundTripper(transport), connection.WithObserver(rec))
			require.NoError(t, fac.Set(connC, testS3()))

			client, err := fac.S3(t.Context(), connC)
			require.NoError(t, err)

			_ = tt.call(t.Context(), client)

			require.NotEmpty(t, rec.all())
			assert.Equal(t, observation{connC, tt.want, resOK}, rec.all()[0])
		})
	}
}

func TestObserver_PutObjectIsPut(t *testing.T) {
	t.Parallel()

	transport := newScripted(step{status: 200, header: http.Header{"Etag": {`"x"`}}})
	rec := &recorder{}
	fac := connection.New(connection.WithRoundTripper(transport), connection.WithObserver(rec))
	require.NoError(t, fac.Set(connC, testS3()))

	client, err := fac.S3(t.Context(), connC)
	require.NoError(t, err)

	_, err = client.PutObject(t.Context(), &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("k")})
	require.NoError(t, err)
	assert.Equal(t, []observation{{connC, opPut, resOK}}, rec.all())
}

func TestObserver_AppliesToOtherAWSClients(t *testing.T) {
	t.Parallel()

	transport := newScripted(
		step{
			status: 200,
			body:   `{"QueueUrls":[]}`,
			header: http.Header{"Content-Type": {"application/x-amz-json-1.0"}},
		},
	)
	rec := &recorder{}
	fac := connection.New(connection.WithRoundTripper(transport), connection.WithObserver(rec))
	require.NoError(t, fac.Set("queue", &connection.AWSSpec{Type: connection.KindSQS, Common: testS3().Common}))

	cfg, err := fac.AWSConfig(t.Context(), "queue")
	require.NoError(t, err)

	_, err = sqs.NewFromConfig(cfg).ListQueues(t.Context(), &sqs.ListQueuesInput{})
	require.NoError(t, err)
	assert.Equal(t, []observation{{"queue", opList, resOK}}, rec.all())
}

func TestObserver_ConnectionNameIsPerConnection(t *testing.T) {
	t.Parallel()

	transport := newScripted(step{status: 200, body: "a"}, step{status: 200, body: "b"})
	rec := &recorder{}
	fac := connection.New(connection.WithRoundTripper(transport), connection.WithObserver(rec))
	require.NoError(t, fac.Replace(map[string]connection.Spec{connOne: testS3(), connTwo: testS3()}))

	for _, name := range []string{connOne, connTwo} {
		client, err := fac.S3(t.Context(), name)
		require.NoError(t, err)

		_, err = client.HeadObject(t.Context(), &s3.HeadObjectInput{Bucket: aws.String("b"), Key: aws.String("k")})
		require.NoError(t, err)
	}

	assert.Equal(t, []observation{{connOne, opHead, resOK}, {connTwo, opHead, resOK}}, rec.all())
}

func TestIsNotModified(t *testing.T) {
	t.Parallel()

	assert.False(t, connection.IsNotModified(nil))
	assert.False(t, connection.IsNotModified(io.EOF))
	assert.True(t, connection.IsNotModified(&smithy.GenericAPIError{Code: "NotModified"}))
	assert.True(t, connection.IsNotModified(fmt.Errorf("wrapped: %w", &smithy.GenericAPIError{Code: "NotModified"})))
	assert.False(t, connection.IsNotModified(&smithy.GenericAPIError{Code: "NoSuchKey"}))
	assert.False(t, connection.IsNotModified(&url.Error{Op: "Get", URL: "x", Err: io.EOF}))
}

func TestObserver_RetryMaxAttemptsIsHonoured(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		maxAttempts int
		wantObs     int
	}{
		{"one attempt", 1, 1},
		{"two attempts", 2, 2},
		{"five attempts", 5, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			steps := make([]step, 5)
			for i := range steps {
				steps[i] = step{status: 503, body: slowDownXML}
			}

			transport := newScripted(steps...)
			rec := &recorder{}
			fac := connection.New(connection.WithRoundTripper(transport), connection.WithObserver(rec))
			require.NoError(
				t,
				fac.Set(connC, other(func(spec *connection.S3Spec) { spec.Retry.MaxAttempts = tt.maxAttempts })),
			)

			client, err := fac.S3(t.Context(), connC)
			require.NoError(t, err)

			_, err = client.HeadObject(t.Context(), &s3.HeadObjectInput{Bucket: aws.String("b"), Key: aws.String("k")})
			require.Error(t, err)
			assert.Len(t, rec.all(), tt.wantObs)
			assert.Equal(t, tt.wantObs, transport.count())
		})
	}
}
