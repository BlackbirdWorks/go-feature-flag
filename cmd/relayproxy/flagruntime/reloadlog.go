package flagruntime

import (
	"context"
	"log/slog"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/notifier"
)

// reloadStats is what one applied round changed. It is computed once per applied round, off the request path.
type reloadStats struct {
	setsCreated, setsUpdated, setsDeleted    int
	flagsCreated, flagsUpdated, flagsDeleted int
}

// tally adds one rebuilt flag set, with the diff that [Engine.enqueue] computed for it, to the counts of the round.
func (stats *reloadStats) tally(item *Rebuilt, diff *notifier.DiffCache) {
	switch {
	case item.New.State == StateWithdrawn:
		stats.setsDeleted++
	case item.Old == nil:
		stats.setsCreated++
	default:
		stats.setsUpdated++
	}

	stats.flagsCreated += len(diff.Added)
	stats.flagsUpdated += len(diff.Updated)
	stats.flagsDeleted += len(diff.Deleted)
}

// objectCounts counts the source objects of a status by state, and the flags that were rejected in them.
func objectCounts(status *Status) (map[ObjectState]int, int) {
	byState := make(map[ObjectState]int, objectStateCount)
	rejected := 0

	for _, obj := range status.Objects {
		byState[obj.State]++
		rejected += len(obj.Rejected)
	}

	return byState, rejected
}

// objectStateCount is the number of states of a source object.
const objectStateCount = 5

// logApplied writes the one line of an applied reload: what changed, the state of the objects and how long the
// round took. It carries counts and names of states only, never an API key or flag content. A rejected round is
// logged by [Engine.logRejected]; a round that changed nothing (an unchanged poll) logs nothing.
func (e *Engine) logApplied(ctx context.Context, res *Reload, stats reloadStats, took time.Duration) {
	if !e.logger.Enabled(ctx, slog.LevelInfo) {
		return
	}

	byState, rejected := objectCounts(e.cur.Status)

	e.logger.LogAttrs(ctx, slog.LevelInfo, "runtime reload",
		slog.String("outcome", string(res.Outcome)), slog.String("code", res.Code),
		slog.Uint64("generation", res.Generation), slog.Uint64("snapshot", res.Snapshot),
		slog.Group("flagsets",
			slog.Int("created", stats.setsCreated), slog.Int("updated", stats.setsUpdated),
			slog.Int("deleted", stats.setsDeleted)),
		slog.Group("flags",
			slog.Int("created", stats.flagsCreated), slog.Int("updated", stats.flagsUpdated),
			slog.Int("deleted", stats.flagsDeleted)),
		slog.Group("objects",
			slog.Int(string(ObjectOK), byState[ObjectOK]), slog.Int(string(ObjectStale), byState[ObjectStale]),
			slog.Int(string(ObjectMissing), byState[ObjectMissing]),
			slog.Int(string(ObjectPending), byState[ObjectPending]),
			slog.Int(string(ObjectInvalid), byState[ObjectInvalid])),
		slog.Int("rejected", rejected), slog.Duration("duration", took))
}

// logRejected writes the one line of a rejected generation, with its stable code and the reason.
func (e *Engine) logRejected(ctx context.Context, res *Reload) {
	e.logger.LogAttrs(ctx, slog.LevelWarn, "runtime reload",
		slog.String("outcome", string(res.Outcome)), slog.String("code", res.Code),
		slog.Uint64("generation", res.Generation), slog.String("detail", res.Detail))
}

// roundDuration is the time since the round started, on the injected clock.
func (e *Engine) roundDuration() time.Duration { return clock.Now().Sub(e.roundStart) }
