package manifestreader

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

// S3Provider gives the S3 client of a named connection. [connection.Factory] implements it. The store asks
// on every call, so a client the factory replaced after a reload is picked up and there is never a second
// client for the connection.
type S3Provider interface {
	S3(ctx context.Context, name string) (*s3.Client, error)
}

// S3Store is an [ObjectStore] on the S3 client of a connection. It issues GetObject and nothing else: no
// HeadObject and no ListObjects.
type S3Store struct {
	provider   S3Provider
	connection string
	bucket     string
	prefix     string
}

// NewS3Store returns the store of bucket on the connection named connection; every key is prefixed with
// prefix (which may be empty).
func NewS3Store(provider S3Provider, connectionName, bucket, prefix string) *S3Store {
	return &S3Store{provider: provider, connection: connectionName, bucket: bucket, prefix: prefix}
}

var _ ObjectStore = (*S3Store)(nil)

// Get implements [ObjectStore] with GetObject and IfNoneMatch.
func (s *S3Store) Get(ctx context.Context, key, etag string, limit int64) (Object, error) {
	client, err := s.provider.S3(ctx, s.connection)
	if err != nil {
		// the connection is unknown or not S3: a configuration problem, not a transient one
		return Object{}, &FetchError{Err: err, Reason: ReasonDenied, Retryable: false}
	}

	input := &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.prefix + key)}
	if etag != "" {
		input.IfNoneMatch = aws.String(etag)
	}

	out, err := client.GetObject(ctx, input)
	if err != nil {
		return Object{}, classifyS3(err)
	}

	defer func() { _ = out.Body.Close() }()

	if aws.ToInt64(out.ContentLength) > limit {
		return Object{}, ErrTooLarge
	}

	// one byte more than the limit tells an over-limit body from one exactly at the limit
	body, err := io.ReadAll(io.LimitReader(out.Body, limit+1))
	if err != nil {
		return Object{}, &FetchError{Err: err, Reason: ReasonError, Retryable: true}
	}

	if int64(len(body)) > limit {
		return Object{}, ErrTooLarge
	}

	return Object{Bytes: body, ETag: aws.ToString(out.ETag)}, nil
}

// classifyS3 maps an SDK error to the results of [ObjectStore]. The 304 comes first: the SDK reports it as an
// error and it is not one.
func classifyS3(err error) error {
	if connection.IsNotModified(err) {
		return ErrNotModified
	}

	status := 0
	if respErr, found := errors.AsType[*awshttp.ResponseError](err); found {
		status = respErr.HTTPStatusCode()
	}

	// a missing bucket is a 404 too, but it is the configuration that is wrong, not the object that is missing
	apiErr, isAPI := errors.AsType[smithy.APIError](err)
	if isAPI && apiErr.ErrorCode() == "NoSuchBucket" {
		return &FetchError{Err: err, Reason: ReasonDenied, Retryable: false}
	}

	_, noKey := errors.AsType[*types.NoSuchKey](err)
	if noKey || status == http.StatusNotFound {
		return ErrNotFound
	}

	switch {
	case status == http.StatusTooManyRequests, status >= http.StatusInternalServerError:
		return &FetchError{Err: err, Reason: ReasonError, Retryable: true}
	case status >= http.StatusBadRequest:
		// 403 and the other client errors, unless the service names a throttle
		if isAPI && throttleCode(apiErr.ErrorCode()) {
			return &FetchError{Err: err, Reason: ReasonError, Retryable: true}
		}

		return &FetchError{Err: err, Reason: ReasonDenied, Retryable: false}
	}

	// no HTTP answer: transport error, timeout, cancellation
	return &FetchError{Err: err, Reason: ReasonError, Retryable: true}
}

func throttleCode(code string) bool {
	switch code {
	case "SlowDown", "Throttling", "ThrottlingException", "RequestLimitExceeded", "RequestTimeout":
		return true
	}

	return false
}
