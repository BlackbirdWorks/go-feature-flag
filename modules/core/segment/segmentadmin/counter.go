package segmentadmin

// KeyCounter counts distinct keys, which a SCAN may return more than once, in memory bounded by its limit.
type KeyCounter struct {
	seen  map[string]struct{}
	limit int
	more  bool
}

// NewKeyCounter returns a counter that keeps at most limit keys.
func NewKeyCounter(limit int) *KeyCounter {
	return &KeyCounter{seen: make(map[string]struct{}, min(limit, scanCount)), limit: limit}
}

// Add counts the keys and tells whether the counter is full: a key beyond the limit is not kept, and More is true.
func (c *KeyCounter) Add(keys []string) bool {
	for _, key := range keys {
		if _, ok := c.seen[key]; ok {
			continue
		}

		if len(c.seen) >= c.limit {
			c.more = true

			return true
		}

		c.seen[key] = struct{}{}
	}

	return false
}

// Count is the distinct keys that were kept.
func (c *KeyCounter) Count() int { return len(c.seen) }

// More tells that a key beyond the limit was seen.
func (c *KeyCounter) More() bool { return c.more }
