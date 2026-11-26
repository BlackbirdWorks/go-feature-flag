package manifestreader

import (
	"context"
	"errors"
)

// Sentinel results of an [ObjectStore] call. Every other failure is a [*FetchError].
var (
	// ErrNotModified is returned by a conditional Get whose ETag is still current. It is not a failure.
	ErrNotModified = errors.New("manifestreader: not modified")
	// ErrNotFound is returned when the object does not exist.
	ErrNotFound = errors.New("manifestreader: object not found")
	// ErrTooLarge is returned when the object is larger than the limit of the call. The store never buffers
	// more than limit bytes.
	ErrTooLarge = errors.New("manifestreader: object too large")
)

// Object is the content of one Get.
type Object struct {
	// ETag is the validator of the content as the store reports it; the reader sends it back on the next
	// conditional Get of the same key. It is not an integrity check.
	ETag string
	// Bytes is the content, at most the limit of the call. The reader never changes it.
	Bytes []byte
}

// GetRequest is one Get. It is a struct so that it can grow without breaking the implementations.
type GetRequest struct {
	// Key is the key of the object.
	Key string
	// ETag makes the request conditional when not empty.
	ETag string
	// VersionID selects an object version when not empty.
	VersionID string
	// Limit is the most bytes the store may read: it never buffers more. Callers that know the size pass it,
	// so a store can allocate the buffer once.
	Limit int64
}

// ObjectStore is the seam between the reader and a backend.
type ObjectStore interface {
	// Get fetches req.Key and reads at most req.Limit bytes. With a non-empty req.ETag the request is
	// conditional and an unchanged object is answered with [ErrNotModified]. A missing object (or version) is
	// [ErrNotFound], a larger one [ErrTooLarge]. Any other failure is a [*FetchError]. Get honours ctx.
	Get(ctx context.Context, req GetRequest) (Object, error)
}

// Failure reasons set by the stores of this package, and used by the reader in its status. The set is closed.
const (
	// ReasonError is a failure that may pass by itself: network, timeout, throttling, 5xx.
	ReasonError = "error"
	// ReasonTimeout is a request that ran into the request timeout of the reader.
	ReasonTimeout = "timeout"
	// ReasonDenied is a failure that a retry does not fix without a change of configuration: 403 and other
	// client errors.
	ReasonDenied = "denied"
)

// FetchError is a failed Get that is neither [ErrNotModified], [ErrNotFound] nor [ErrTooLarge].
type FetchError struct {
	// Err is the cause. It may name the bucket or the key; the reader never logs or reports it, only Reason.
	Err error
	// Reason is the closed code the reader reports: [ReasonError] or [ReasonDenied].
	Reason string
	// Retryable tells whether trying again can succeed without a change of configuration.
	Retryable bool
}

// Error implements error.
func (e *FetchError) Error() string {
	if e.Err == nil {
		return "manifestreader: fetch failed: " + e.Reason
	}

	return "manifestreader: fetch failed: " + e.Reason + ": " + e.Err.Error()
}

// Unwrap returns the cause.
func (e *FetchError) Unwrap() error { return e.Err }
