package events

import (
	"fmt"
	"log/slog"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// Sealed holds an engine exporter or notifier, which carries its secrets in clear (a URL, a signing key). It
// prints the redaction marker with every fmt verb and as a slog value, so that a definition that is logged or
// formatted by mistake does not show them. [Sealed.Unseal] gives the value back.
type Sealed[T any] struct {
	value T
}

// Seal wraps value.
func Seal[T any](value T) Sealed[T] { return Sealed[T]{value: value} }

// Unseal returns the wrapped value. Do not print it.
func (s Sealed[T]) Unseal() T { return s.value } //nolint:ireturn // the type parameter

// String implements [fmt.Stringer].
func (Sealed[T]) String() string { return bootstrap.RedactedValue }

// GoString implements [fmt.GoStringer].
func (Sealed[T]) GoString() string { return bootstrap.RedactedValue }

// Format implements [fmt.Formatter] so that no verb prints the content.
func (Sealed[T]) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(bootstrap.RedactedValue)) }

// LogValue implements [slog.LogValuer].
func (Sealed[T]) LogValue() slog.Value { return slog.StringValue(bootstrap.RedactedValue) }
