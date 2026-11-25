package flagdef

import "sync"

// loadPtr returns the *T stored under key in m. ok is false when m has no entry for key, or when the entry is not a
// *T: the maps of this package only ever store one type, so a mismatch is handled as a missing entry.
func loadPtr[T any](m *sync.Map, key any) (*T, bool) {
	v, ok := m.Load(key)
	if !ok {
		return nil, false
	}

	typed, ok := v.(*T)
	if !ok {
		return nil, false
	}

	return typed, true
}

// ptrOr returns v as a *T, or fallback when v is not a *T.
func ptrOr[T any](v any, fallback *T) *T {
	typed, ok := v.(*T)
	if !ok {
		return fallback
	}

	return typed
}
