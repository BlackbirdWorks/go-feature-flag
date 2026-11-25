package flag

import "sync"

// loadAs returns the value of key in m as a T. ok is false when m has no entry for key, or when the entry is not a T:
// the maps of this package only ever store one type, so a mismatch is handled as a missing entry.
//
//nolint:ireturn // generic: the result is a type parameter
func loadAs[T any](m *sync.Map, key any) (T, bool) {
	var zero T

	v, ok := m.Load(key)
	if !ok {
		return zero, false
	}

	typed, ok := v.(T)
	if !ok {
		return zero, false
	}

	return typed, true
}

// asOr returns v as a T, or fallback when v is not a T.
//
//nolint:ireturn // generic: the result is a type parameter
func asOr[T any](v any, fallback T) T {
	typed, ok := v.(T)
	if !ok {
		return fallback
	}

	return typed
}
