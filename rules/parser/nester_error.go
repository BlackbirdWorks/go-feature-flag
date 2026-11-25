package parser

import (
	"encoding/json"
	"fmt"
	"maps"
)

const (
	err3 = "err"
	msg2 = "msg"
)

// ErrVals holds the key-value context attached to a NestedError.
type ErrVals map[string]any

// Dupe returns a shallow copy of the values.
func (e ErrVals) Dupe() ErrVals {
	ret := make(ErrVals)
	maps.Copy(ret, e)

	return ret
}

// Merge copies the entries of vals into e.
func (e ErrVals) Merge(vals ErrVals) {
	maps.Copy(e, vals)
}

// NestedError wraps an error with a message and the values that describe it.
type NestedError struct {
	Err  error
	Vals ErrVals
	Msg  string
}

// Original returns the innermost error of a chain of NestedError values.
func (e *NestedError) Original() error {
	// Exact type, not errors.As: an error that merely wraps a NestedError is the original.
	//nolint:errorlint // exact *NestedError check stops unwrapping outer wrappers
	if nested, ok := e.Err.(*NestedError); ok {
		return nested.Original()
	}

	return e.Err
}

// Error renders Vals plus "err" and "msg" as JSON. It marshals a copy so
// e is never written to and concurrent Error calls are safe.
func (e *NestedError) Error() string {
	vals := e.Vals.Dupe()
	vals[err3] = e.Err.Error()
	vals[msg2] = e.Msg

	data, err := json.Marshal(vals)
	if err != nil {
		return fmt.Sprintf("%s: %s", e.Msg, e.Err.Error())
	}

	return string(data)
}

// Set merges vals into the values of the error and returns the error.
func (e *NestedError) Set(vals ErrVals) *NestedError {
	if e.Vals == nil {
		e.Vals = make(ErrVals)
	}

	e.Vals.Merge(vals.Dupe())

	return e
}

func newNestedError(err error, msg string) *NestedError {
	return &NestedError{
		Err: err,
		Msg: msg,
	}
}
