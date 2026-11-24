package internalerror

import "fmt"

// NestedKeyNotFoundError is the error returned when a nested key is not found.
type NestedKeyNotFoundError struct {
	Key string
}

// Error returns the text of the error.
func (e *NestedKeyNotFoundError) Error() string {
	return fmt.Sprintf("nested key not found: %s", e.Key)
}
