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
}

// NewS3Store returns the store of bucket on the connection named connectionName. Keys are absolute bucket keys,
// as the publisher writes them (a key prefix is applied by the publisher only): the store fetches exactly the key
// it is given and never adds a prefix.
func NewS3Store(provider S3Provider, connectionName, bucket string) *S3Store {
	return &S3Store{provider: provider, connection: connectionName, bucket: bucket}
}

var _ ObjectStore = (*S3Store)(nil)

// Get implements [ObjectStore] with GetObject, IfNoneMatch and VersionId. The body is read into one buffer
// sized from the Content-Length (or from the limit when the answer has none), never beyond the limit.
func (s *S3Store) Get(ctx context.Context, req GetRequest) (Object, error) {
	client, err := s.provider.S3(ctx, s.connection)
	if err != nil {
		// the connection is unknown or not S3: a configuration problem, not a transient one
		return Object{}, &FetchError{Err: err, Reason: ReasonDenied, Retryable: false}
	}

	input := &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(req.Key)}
	if req.ETag != "" {
		input.IfNoneMatch = aws.String(req.ETag)
	}

	if req.VersionID != "" {
		input.VersionId = aws.String(req.VersionID)
	}

	out, err := client.GetObject(ctx, input)
	if err != nil {
		return Object{}, classifyS3(err, req.VersionID != "")
	}

	defer func() { _ = out.Body.Close() }()

	size := req.Limit + 1 // one byte more than the limit tells an over-limit body from one at the limit

	if out.ContentLength != nil {
		if *out.ContentLength > req.Limit {
			return Object{}, ErrTooLarge
		}

		size = *out.ContentLength
	}

	buf := make([]byte, size)

	read, err := io.ReadFull(out.Body, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return Object{}, &FetchError{Err: err, Reason: ReasonError, Retryable: true}
	}

	if out.ContentLength != nil && int64(read) < size {
		// the body ended before its Content-Length: a broken transfer
		return Object{}, &FetchError{Err: io.ErrUnexpectedEOF, Reason: ReasonError, Retryable: true}
	}

	if int64(read) > req.Limit {
		return Object{}, ErrTooLarge
	}

	return Object{Bytes: buf[:read], ETag: aws.ToString(out.ETag)}, nil
}

// classifyS3 maps an SDK error to the results of [ObjectStore]. The 304 comes first: the SDK reports it as an
// error and it is not one.
func classifyS3(err error, withVersion bool) error {
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

	if isNotFound(err, status, withVersion) {
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

// isNotFound reports a missing object or version. A version id that does not exist is 404 NoSuchKey on the
// emulator and 400 InvalidArgument on AWS.
func isNotFound(err error, status int, withVersion bool) bool {
	_, noKey := errors.AsType[*types.NoSuchKey](err)
	if noKey || status == http.StatusNotFound {
		return true
	}

	apiErr, isAPI := errors.AsType[smithy.APIError](err)

	return withVersion && isAPI && apiErr.ErrorCode() == "InvalidArgument"
}
