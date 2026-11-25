package evalcore

import (
	"maps"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
)

const (
	// reservedContextKeys is the room kept in the map of a context for the keys that are not custom ones.
	reservedContextKeys = 3
)

// ContextToMap convert the context to a MAP to use the query on it.
func ContextToMap(ctx ffcontext.Context) map[string]any {
	return ContextToMapInto(make(map[string]any, len(ctx.GetCustom())+reservedContextKeys), ctx)
}

// ContextToMapInto is ContextToMap filling (and returning) the empty map dst, that the caller can
// reuse to avoid allocating a map for every evaluation.
func ContextToMapInto(dst map[string]any, ctx ffcontext.Context) map[string]any {
	// We don't have a json copy of the user.
	custom := ctx.GetCustom()
	userCopy := dst

	// Duplicate the map to keep User un-mutable
	maps.Copy(userCopy, custom)
	userCopy["anonymous"] = ctx.IsAnonymous()
	// box the key once for both entries
	k := any(ctx.GetKey())
	userCopy["key"] = k
	userCopy["targetingKey"] = k

	return userCopy
}
