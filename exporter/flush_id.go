package exporter

import "context"

type flushIDKey struct{}

func withFlushID(ctx context.Context, id uint64) context.Context {
	return context.WithValue(ctx, flushIDKey{}, id)
}

// FlushID is the number of the flush that a batch belongs to, from the context the event store gives to the
// exporter: the batches of one flush share it. It is 0 for a call that is not part of a numbered flush.
func FlushID(ctx context.Context) uint64 {
	id, _ := ctx.Value(flushIDKey{}).(uint64)

	return id
}
