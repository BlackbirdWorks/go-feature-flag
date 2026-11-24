package flag

import (
	"runtime"
	"sync"
	"sync/atomic"
	"weak"
)

// scheduledMemo memoizes, for each flag with a scheduled rollout, the flag obtained by applying
// the n first due steps (n = 0 .. number of steps), because that is all the result depends on.
// Building it needs a deep copy of the flag, which is far more expensive than the evaluation.
//
// A flag is identified by the identity of its Scheduled steps (shared by the copies of the
// InternalFlag struct), through a weak pointer: the entry does not keep the flag alive and is
// removed when the steps are garbage collected, so a reloaded configuration does not leak.
// The memoized flags are read-only. The steps of a flag must not be modified once it is evaluated.
// A Service owns one memo.
type scheduledMemo struct {
	entries sync.Map // weak.Pointer[[]ScheduledStep] -> *scheduledEntry
}

type scheduledEntry struct {
	byCount []atomic.Pointer[InternalFlag]
}

func (m *scheduledMemo) get(
	f *InternalFlag, due int, build func() (*InternalFlag, error),
) (*InternalFlag, error) {
	steps := f.Scheduled
	key := weak.Make(steps)
	v, ok := m.entries.Load(key)
	if !ok {
		entry := &scheduledEntry{byCount: make([]atomic.Pointer[InternalFlag], len(*steps)+1)}
		var loaded bool
		v, loaded = m.entries.LoadOrStore(key, entry)
		if !loaded {
			runtime.AddCleanup(steps, func(k weak.Pointer[[]ScheduledStep]) { m.entries.Delete(k) }, key)
		}
	}
	entry := v.(*scheduledEntry)
	if due >= len(entry.byCount) { // the steps changed after the entry was created
		return build()
	}
	if cached := entry.byCount[due].Load(); cached != nil {
		return cached, nil
	}
	built, err := build()
	if err != nil {
		return built, err
	}
	entry.byCount[due].Store(built)

	return built, nil
}
