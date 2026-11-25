package internalerror

// NestedKeyNotFoundError is the error returned when a nested key is not found.
type NestedKeyNotFoundError struct {
	Key string
}

// Error returns the text of the error.
func (e *NestedKeyNotFoundError) Error() string {
	return "nested key not found: " + e.Key
}
