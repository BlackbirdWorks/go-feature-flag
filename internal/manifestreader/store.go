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
	// Limit is the largest body the store returns: it never returns, nor keeps, more than Limit bytes. A store
	// may read one byte past it to tell an object of exactly Limit bytes from a larger one. A negative limit is
	// a limit of zero: only an empty object is returned. Callers that know the size pass it,
	// so a store can allocate the buffer once.
	Limit int64
}

// ObjectStore is the seam between the reader and a backend. The conformance suite in the storetest package
// executes this contract; a store passes when storetest.Run passes on it.
//
// The contract in short:
//   - The ETag of a result describes exactly the bytes returned with it, differs when the bytes differ, and a
//     conditional Get that holds the current ETag is answered with [ErrNotModified] (and a zero [Object]). Equal
//     bytes need not give an equal ETag: a row version, a commit SHA or a generation number is a valid ETag.
//   - A failed Get returns a zero [Object]. A Get that ended with the context returns an error that wraps the
//     context error.
//   - When Limit is below the size of the object, the answer is [ErrTooLarge], or [ErrNotModified] if the request
//     is conditional with the current ETag and the store answers before it looks at the size. Never a body.
//   - A negative Limit is a Limit of zero.
//   - The bytes returned are the caller's: a later write to the object never changes bytes already returned.
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
	// Throttled marks a throttling answer (429, SlowDown): the reader backs off one step further than for
	// another error.
	Throttled bool
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
