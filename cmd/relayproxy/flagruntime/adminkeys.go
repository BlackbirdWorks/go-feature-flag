package flagruntime

import (
	"fmt"
	"slices"
	"strings"
)

// maxNamedCollisions caps the flag sets that the detail of a rejection names.
const maxNamedCollisions = 10

// collidingSets returns the names of the flag sets that hold an API key which is also an admin key, sorted. It
// compares the keys by value and never returns, prints or logs one.
func collidingSets(admin map[string]struct{}, byKey map[string]*FlagSet) []string {
	var names []string

	add := func(set *FlagSet) {
		if set != nil && !slices.Contains(names, set.Name) {
			names = append(names, set.Name)
		}
	}

	if len(admin) <= len(byKey) {
		for key := range admin {
			add(byKey[key])
		}
	} else {
		for key, set := range byKey {
			if _, ok := admin[key]; ok {
				add(set)
			}
		}
	}

	slices.Sort(names)

	return names
}

// collisionPaths addresses the API keys of the flag sets by path, for example flagSets[a].apiKeys. The names of the
// flag sets are public; the keys are never part of it.
func collisionPaths(names []string) string {
	shown := names[:min(len(names), maxNamedCollisions)]
	paths := make([]string, 0, len(shown)+1)

	for _, name := range shown {
		paths = append(paths, "flagSets["+name+"].apiKeys")
	}

	if len(names) > len(shown) {
		paths = append(paths, fmt.Sprintf("%d more", len(names)-len(shown)))
	}

	return strings.Join(paths, ", ")
}

// vetAdminKeys rejects what would make an admin key and an API key of a flag set the same string: the content when
// the flag sets it builds hold an admin key (of the bootstrap file of the round, else the running one), and the
// bootstrap file when its admin keys equal a key of the running flag sets. A rejection keeps the running
// runtime (last known good) and names the flag sets, never a key. Flag-set keys arrive with the published documents
// and the admin keys with the bootstrap file, so the check is made here, once per round, on the candidate.
func (e *Engine) vetAdminKeys(plan *contentPlan, boot *bootPlan) {
	if boot.apply {
		names := collidingSets(boot.admin, e.cur.ByKey)
		if len(names) > 0 {
			res := e.rejectCollision(e.cur.Generation,
				"auth.adminKeys: equals an API key of "+collisionPaths(names))
			*boot = bootPlan{res: res}
		}
	}

	if plan.built == nil {
		return
	}

	admin := e.cur.AdminKeys
	if boot.apply {
		admin = boot.admin
	}

	names := collidingSets(admin, plan.built.Runtime.ByKey)
	if len(names) == 0 {
		return
	}

	res := e.rejectCollision(plan.built.Runtime.Generation,
		collisionPaths(names)+": an entry of auth.adminKeys equals an API key")
	*plan = contentPlan{res: res}
}

func (e *Engine) rejectCollision(generation uint64, detail string) Reload {
	res := Reload{
		Outcome: OutcomeRejected, Code: CodeAdminKeyCollision, Generation: generation,
		Detail: fmt.Sprintf("%s: %s", ErrAdminKeyCollision, detail),
	}
	e.noteRejected(res)

	return res
}
