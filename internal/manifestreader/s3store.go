package manifestreader

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"slices"

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

	limit := max(req.Limit, 0) // a negative limit is a zero one: see GetRequest.Limit

	out, err := client.GetObject(ctx, input)
	if err != nil {
		return Object{}, classifyS3(err, req.VersionID != "")
	}

	defer func() { _ = out.Body.Close() }()

	buf, err := readBody(out.Body, out.ContentLength, limit)
	if err != nil {
		return Object{}, err
	}

	return Object{Bytes: buf, ETag: aws.ToString(out.ETag)}, nil
}

// readBody reads the body. With a Content-Length it reads into one buffer of exactly that size. Without one it
// reads at most limit plus one byte (which tells an over-limit body from one at the limit), growing as the body
// arrives, and returns a buffer of exactly the size of the body, so that a body much smaller than the limit does
// not keep a limit-sized buffer. It never returns more than limit bytes.
func readBody(body io.Reader, contentLength *int64, limit int64) ([]byte, error) {
	if contentLength == nil {
		return readUnknownLength(body, limit)
	}

	if *contentLength > limit {
		return nil, ErrTooLarge
	}

	buf := make([]byte, *contentLength)

	read, err := io.ReadFull(body, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, &FetchError{Err: err, Reason: ReasonError, Retryable: true}
	}

	if int64(read) < *contentLength {
		// the body ended before its Content-Length: a broken transfer
		return nil, &FetchError{Err: io.ErrUnexpectedEOF, Reason: ReasonError, Retryable: true}
	}

	return buf, nil
}

// spareDivisor makes the spare capacity that [rightSize] keeps a quarter of the body.
const spareDivisor = 4

// readUnknownLength reads a body whose length the answer did not give.
func readUnknownLength(body io.Reader, limit int64) ([]byte, error) {
	window := limit
	if window < math.MaxInt64 {
		window++
	}

	data, err := io.ReadAll(io.LimitReader(body, window))
	if err != nil {
		return nil, &FetchError{Err: err, Reason: ReasonError, Retryable: true}
	}

	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}

	return rightSize(data), nil
}

// rightSize returns data with no spare capacity. When the spare capacity is small it only caps the capacity (no
// second buffer: the peak stays near the size of the body); when it is large (a small body that ReadAll grew past)
// it copies into a buffer of exactly the size, which is cheap because the body is small.
func rightSize(data []byte) []byte {
	if spare := cap(data) - len(data); spare <= len(data)/spareDivisor {
		return slices.Clip(data)
	}

	out := make([]byte, len(data))
	copy(out, data)

	return out
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
		return &FetchError{
			Err: err, Reason: ReasonError, Retryable: true,
			Throttled: status == http.StatusTooManyRequests || isAPI && throttleCode(apiErr.ErrorCode()),
		}
	case status >= http.StatusBadRequest:
		// 403 and the other client errors, unless the service names a throttle
		if isAPI && throttleCode(apiErr.ErrorCode()) {
			return &FetchError{Err: err, Reason: ReasonError, Retryable: true, Throttled: true}
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
