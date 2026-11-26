package s3publish

import (
	"errors"
	"net/http"

	"github.com/aws/smithy-go"
)

// httpStatus returns the HTTP status carried by err, or 0.
func httpStatus(err error) int {
	var withStatus interface{ HTTPStatusCode() int }
	if errors.As(err, &withStatus) {
		return withStatus.HTTPStatusCode()
	}

	return 0
}

// errorCode returns the S3 error code carried by err, or "".
func errorCode(err error) string {
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		return apiErr.ErrorCode()
	}

	return ""
}

// isNotFound reports a missing key (404 NoSuchKey or NotFound).
func isNotFound(err error) bool {
	if err == nil {
		return false
	}

	switch errorCode(err) {
	case "NoSuchKey", "NotFound":
		return true
	}

	return httpStatus(err) == http.StatusNotFound
}

// isPrecondition reports a failed condition: 412 PreconditionFailed, or 409 ConditionalRequestConflict that AWS
// returns when conditional writes race.
func isPrecondition(err error) bool {
	if err == nil {
		return false
	}

	switch errorCode(err) {
	case "PreconditionFailed", "ConditionalRequestConflict":
		return true
	}

	switch httpStatus(err) {
	case http.StatusPreconditionFailed, http.StatusConflict:
		return true
	}

	return false
}
