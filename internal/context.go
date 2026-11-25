package internal

import "context"

// ContextOrBackground returns ctx, or the background context when ctx is nil. Passing a nil context is a mistake
// of the caller (staticcheck SA1012), the entry points of the library tolerate it rather than panic.
func ContextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}

	return ctx
}
