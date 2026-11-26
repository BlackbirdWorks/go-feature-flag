package manifestreader

// Counts are the numbers of objects of the held manifest per state.
type Counts struct {
	OK      int
	Stale   int
	Missing int
}

// HookPanic is the count of recovered panics of one hook.
type HookPanic struct {
	// Name is "restore", "save" or "onRound".
	Name  string
	Count uint64
}

// CacheStatus counts the verified objects the reader holds, each content once however many names use it.
type CacheStatus struct {
	Objects int
	Bytes   int64
}
