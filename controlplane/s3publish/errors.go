package s3publish

import (
	"errors"
	"fmt"
)

// Sentinel errors of the package. Test them with [errors.Is].
var (
	// ErrInvalid wraps every validation failure of the documents, the manifest or the configuration: nothing was
	// written. The wrapped error is the controlplane error.
	ErrInvalid = errors.New("s3publish: invalid documents")
	// ErrConfig is returned by New for an unusable configuration.
	ErrConfig = errors.New("s3publish: invalid configuration")
	// ErrConflict is returned when another publisher changed the manifest after it was read (HTTP 412, 409, or the
	// manifest was deleted). The bucket holds a complete state; re-read and publish again.
	ErrConflict = errors.New("s3publish: the manifest changed since it was read")
	// ErrGeneration is returned when the requested generation does not strictly increase over the manifest in the
	// bucket.
	ErrGeneration = errors.New("s3publish: generation does not increase")
	// ErrChannelMismatch is returned when the manifest in the bucket belongs to another channel.
	ErrChannelMismatch = errors.New("s3publish: the manifest in the bucket has another channel")
	// ErrPreviousManifest is returned when the manifest in the bucket cannot be read or is invalid, so its
	// generation is unknown. The publisher never overwrites it.
	ErrPreviousManifest = errors.New("s3publish: the manifest in the bucket is not readable")
	// ErrObjectMismatch is returned when an object exists under its content-addressed key with another size or
	// checksum. The publisher never overwrites an object.
	ErrObjectMismatch = errors.New("s3publish: an object exists with other content")
	// ErrNoManifest is returned by Verify when the bucket holds no manifest.
	ErrNoManifest = errors.New("s3publish: no manifest in the bucket")
)

// OperationError is a failed S3 call that is not a conflict: a transport failure, a throttle, an access error.
// The call is safe to repeat after it.
type OperationError struct {
	// Err is the error of the client.
	Err error
	// Operation is GetObject, HeadObject or PutObject.
	Operation string
	// Key is the object key.
	Key string
}

// Error implements error.
func (e *OperationError) Error() string {
	return fmt.Sprintf("s3publish: %s %s: %v", e.Operation, e.Key, e.Err)
}

// Unwrap returns the error of the client.
func (e *OperationError) Unwrap() error { return e.Err }
