package flagruntime

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// CodeAdminKeyCollision is the code of a [Problem] that says an API key of the flag set is also an admin key. The
// key grants nothing while the collision exists: it is not routed to the flag set and it is not an admin key (it is
// answered as an unknown key). The other keys of the flag set and the other admin keys work, and the generation is
// applied. It is reported per key owner like [controlplane.CodeDuplicateKey], never as a rejected
// generation, so a published key can never keep the relay from serving.
const CodeAdminKeyCollision controlplane.Code = "admin-key-collision"

// heldRouting is the routing a build produced, before the admin keys are applied: the engine keeps it, so a reload
// of the bootstrap file derives the served routing again from the content it already holds.
type heldRouting struct {
	byKey map[string]*FlagSet
	sets  map[string]*FlagSetStatus
}

// held returns the routing of the content that runs, before the admin keys.
func (e *Engine) held() heldRouting {
	if e.heldByKey == nil {
		return heldRouting{byKey: e.cur.ByKey, sets: e.cur.Status.FlagSets}
	}

	return heldRouting{byKey: e.heldByKey, sets: e.heldSets}
}

// deriveRouting removes from the routing the keys that are admin keys, and marks the flag sets that lost one with
// a problem. A key that is both grants nothing: flag-set keys are given to clients and are not secrets, so one must
// never become an admin credential, and it is dropped from the admin keys as well. The held routing and the admin
// keys are not changed; without a collision they are returned as they are. It returns the names of the flag sets
// that lost a key, sorted. Keys are compared by value and are never returned, printed or logged.
func deriveRouting(admin map[string]struct{}, held heldRouting) (heldRouting, map[string]struct{}, []string) {
	lost := map[string]int{}

	add := func(set *FlagSet) {
		if set != nil {
			lost[set.Name]++
		}
	}

	if len(admin) <= len(held.byKey) {
		for key := range admin {
			add(held.byKey[key])
		}
	} else {
		for key, set := range held.byKey {
			if _, ok := admin[key]; ok {
				add(set)
			}
		}
	}

	if len(lost) == 0 {
		return held, admin, nil
	}

	byKey := maps.Clone(held.byKey)
	enabled := maps.Clone(admin)

	for key := range admin {
		if _, ok := byKey[key]; ok {
			delete(byKey, key)
			delete(enabled, key)
		}
	}

	sets := maps.Clone(held.sets)
	names := make([]string, 0, len(lost))

	for name, count := range lost {
		names = append(names, name)

		old := sets[name]
		if old == nil {
			continue
		}

		status := *old
		status.APIKeys = max(0, status.APIKeys-count)
		status.Problems = append(slices.Clone(status.Problems), Problem{
			Code:   CodeAdminKeyCollision,
			Reason: "an API key of this flag set is also an admin key: neither is accepted",
		})

		if status.State == StateServing {
			status.State = StateDegraded
		}

		sets[name] = &status
	}

	slices.Sort(names)

	return heldRouting{byKey: byKey, sets: sets}, enabled, names
}

// logCollisions writes one error when a collision is new: a flag set that was not in the previous generation, or more
// disabled admin keys than before (a second key of a flag set that already collided, which the flag set names alone
// cannot show). A distinct error says when every admin key is disabled, once until a key works again. It names the
// flag sets and counts, never a key. prior is what the generation that ran before disabled.
func (e *Engine) logCollisions(
	ctx context.Context, generation uint64, names []string, disabled int, prior disabledKeys,
) {
	var fresh []string

	for _, name := range names {
		if _, known := e.collided[name]; !known {
			fresh = append(fresh, name)
		}
	}

	e.collided = make(map[string]struct{}, len(names))
	for _, name := range names {
		e.collided[name] = struct{}{}
	}

	if len(fresh) > 0 || disabled > prior.count {
		e.logger.LogAttrs(
			ctx,
			slog.LevelError,
			"an admin key is disabled because a flag set publishes the same key; the key is not routed to the flag set either",
			slog.String("code", string(CodeAdminKeyCollision)),
			slog.Uint64("generation", generation),
			slog.String("flagsets", strings.Join(names, ",")),
			slog.Int("disabledAdminKeys", disabled),
		)
	}

	if disabled > 0 && disabled >= len(e.rawAdmin()) && !prior.all {
		e.logger.LogAttrs(
			ctx,
			slog.LevelError,
			"every admin key is disabled because flag sets publish the same keys; "+
				"the admin API is unreachable until a key is rotated",
			slog.String("code", string(CodeAdminKeyCollision)),
			slog.Uint64("generation", generation),
			slog.Int("disabledAdminKeys", disabled),
		)
	}
}

// disabledKeys is what a generation had disabled among the admin keys.
type disabledKeys struct {
	// count is the number of disabled admin keys, and all says that none was left.
	count int
	all   bool
}

// disabledAdminKeys reads what the running generation disabled. Call it before the swap of the next one.
func (e *Engine) disabledAdminKeys() disabledKeys {
	if e.cur == nil {
		return disabledKeys{}
	}

	count := e.cur.Status.DisabledAdminKeys

	return disabledKeys{count: count, all: count > 0 && count >= len(e.rawAdmin())}
}

// rawAdmin returns the admin keys of the bootstrap file that runs, before the keys that a flag set also publishes
// are disabled.
func (e *Engine) rawAdmin() map[string]struct{} {
	if e.heldAdmin == nil {
		return e.cur.AdminKeys
	}

	return e.heldAdmin
}
