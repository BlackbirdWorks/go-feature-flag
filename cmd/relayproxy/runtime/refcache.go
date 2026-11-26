package runtime //nolint:revive,nolintlint // the name is fixed: the package is cmd/relayproxy/runtime

// refEntry is a cached value and the number of objects that hold it.
type refEntry[T any] struct {
	value T
	refs  int
}

// refCache keeps parse results by checksum. An entry lives while at least one object of the current content
// refers to it, so the cache survives across generations and drops what is no longer referenced.
type refCache[T any] struct {
	m map[string]*refEntry[T]
}

func newRefCache[T any]() refCache[T] {
	return refCache[T]{m: map[string]*refEntry[T]{}}
}

// lookup returns the entry of a key, or nil.
func (c *refCache[T]) lookup(key string) *refEntry[T] {
	return c.m[key]
}

// apply stores the staged values, then applies the reference changes and drops the entries that nothing holds.
func (c *refCache[T]) apply(staged map[string]T, inc, dec []string) {
	for key, value := range staged {
		if _, ok := c.m[key]; !ok {
			c.m[key] = &refEntry[T]{value: value}
		}
	}

	for _, key := range inc {
		if entry, ok := c.m[key]; ok {
			entry.refs++
		}
	}

	for _, key := range dec {
		entry, ok := c.m[key]
		if !ok {
			continue
		}

		entry.refs--
		if entry.refs <= 0 {
			delete(c.m, key)
		}
	}
}

func (c *refCache[T]) len() int { return len(c.m) }
