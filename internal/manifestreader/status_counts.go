package manifestreader

// Counts are the numbers of objects of the held manifest per state.
type Counts struct {
	OK      int `json:"ok"`
	Stale   int `json:"stale"`
	Missing int `json:"missing"`
}

// HookPanic is the count of recovered panics of one hook.
type HookPanic struct {
	// Name is "restore", "save" or "onRound".
	Name  string `json:"name"`
	Count uint64 `json:"count"`
}

// CacheStatus counts the verified objects the reader holds, each content once however many names use it.
type CacheStatus struct {
	Objects int   `json:"objects"`
	Bytes   int64 `json:"bytes"`
}
