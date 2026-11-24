package internalerror

import "fmt"

// EmptyBucketingKeyError is the error returned when the bucketing key is empty.
type EmptyBucketingKeyError struct {
	Message string
}

// Error returns the text of the error.
func (e *EmptyBucketingKeyError) Error() string {
	return fmt.Sprintf("Error: %s", e.Message)
}
