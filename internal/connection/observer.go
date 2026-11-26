package connection

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// The results reported to a [RequestObserver]: a closed set.
const (
	// ResultOK is a 2xx answer.
	ResultOK = "ok"
	// ResultNotModified is a 304 answer to a conditional request. It is not a failure.
	ResultNotModified = "not_modified"
	// ResultNotFound is a 404 answer.
	ResultNotFound = "not_found"
	// ResultThrottled is a 429 answer, or an error whose code is a throttling code (for example SlowDown).
	ResultThrottled = "throttled"
	// ResultClientError is any other 4xx answer (403, 400, ...).
	ResultClientError = "client_error"
	// ResultRedirect is a 3xx answer other than 304: the factory clients never follow redirects, so the SDK
	// gets the redirect itself and the observation is the one wire request.
	ResultRedirect = "redirect"
	// ResultServerError is a 5xx answer that is not a throttle.
	ResultServerError = "server_error"
	// ResultError is an attempt without a usable answer: transport error, timeout, cancellation, or a 2xx
	// answer the SDK could not decode.
	ResultError = "error"
)

// The operation labels reported to a [RequestObserver]: a closed set, derived from the SDK operation name.
const (
	OperationGet    = "get"
	OperationList   = "list"
	OperationHead   = "head"
	OperationPut    = "put"
	OperationDelete = "delete"
	OperationOther  = "other"
)

// RequestObserver is told about every HTTP attempt made by the clients of a [Factory]: once per attempt, so
// an operation retried after a 503 is observed twice. An observation is an attempt of the SDK, not a write
// on the wire: the Go transport may replay an idempotent request on a kept-alive connection that died before
// any response byte, without another observation. The service answered none of the replayed requests, so
// it processed at most one of them. It is called on the goroutine of the request and must
// not block. The relay maps it to semaphore_s3_requests_total.
type RequestObserver interface {
	// Observe records one attempt of connection, with an operation (an Operation* constant) and a
	// result (a Result* constant).
	Observe(connection, operation, result string)
}

// IsNotModified reports whether err is the error the SDK returns for a conditional request answered with
// 304: an HTTP 304 in the response error, or the NotModified API error code. A caller checks it before any
// logging or counting, so a 304 is never handled as a failure.
func IsNotModified(err error) bool {
	if err == nil {
		return false
	}

	respErr, found := errors.AsType[*awshttp.ResponseError](err)
	if found && respErr.HTTPStatusCode() == http.StatusNotModified {
		return true
	}

	var apiErr smithy.APIError

	return errors.As(err, &apiErr) && apiErr.ErrorCode() == "NotModified"
}

// observerMiddlewareID names the middleware in the SDK stack.
const observerMiddlewareID = "SemaphoreRequestObserver"

// observerOption registers the observer middleware in the Deserialize step. It sits outside the operation
// deserializer so it sees both the raw HTTP status and the decoded error code, and inside the Retry
// middleware (Finalize step) so it runs once per attempt.
func observerOption(name string, obs RequestObserver) func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Deserialize.Add(middleware.DeserializeMiddlewareFunc(observerMiddlewareID,
			func(ctx context.Context, in middleware.DeserializeInput, next middleware.DeserializeHandler,
			) (middleware.DeserializeOutput, middleware.Metadata, error) {
				out, meta, err := next.HandleDeserialize(ctx, in)

				status := 0
				if resp, ok := out.RawResponse.(*smithyhttp.Response); ok && resp != nil {
					status = resp.StatusCode
				}

				obs.Observe(name, operationLabel(middleware.GetOperationName(ctx)), classify(status, err))

				return out, meta, err //nolint:wrapcheck // the SDK error goes back to the SDK unchanged
			}), middleware.Before)
	}
}

func operationLabel(operation string) string {
	switch {
	case strings.HasPrefix(operation, "Get"):
		return OperationGet
	case strings.HasPrefix(operation, "List"):
		return OperationList
	case strings.HasPrefix(operation, "Head"):
		return OperationHead
	case strings.HasPrefix(operation, "Put"):
		return OperationPut
	case strings.HasPrefix(operation, "Delete"):
		return OperationDelete
	}

	return OperationOther
}

// classify maps the HTTP status of an attempt (0 when there was no response) and the error the SDK produced
// to a Result. The 304 comes first: the SDK reports it as an error, and it is not one.
func classify(status int, err error) string {
	if status == http.StatusNotModified || IsNotModified(err) {
		return ResultNotModified
	}

	if status == 0 {
		if re, ok := errors.AsType[*awshttp.ResponseError](err); ok {
			status = re.HTTPStatusCode()
		}
	}

	if status == 0 {
		return ResultError
	}

	if status >= http.StatusMultipleChoices && status < http.StatusBadRequest {
		return ResultRedirect
	}

	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		if err != nil {
			return ResultError
		}

		return ResultOK
	}

	return classifyFailure(status, err)
}

func classifyFailure(status int, err error) string {
	switch {
	case status == http.StatusNotFound:
		return ResultNotFound
	case status == http.StatusTooManyRequests || isThrottleCode(err):
		return ResultThrottled
	case status >= http.StatusBadRequest && status < http.StatusInternalServerError:
		return ResultClientError
	case status >= http.StatusInternalServerError:
		return ResultServerError
	}

	return ResultError
}

func isThrottleCode(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}

	_, ok := retry.DefaultThrottleErrorCodes[apiErr.ErrorCode()]

	return ok
}
