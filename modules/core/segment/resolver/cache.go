package resolver

import (
	"hash/maphash"
	"sync"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// entry is one cached answer, in the list of its shard.
type entry struct {
	expires time.Time
	prev    *entry
	next    *entry
	key     string
	state   segment.State
}

// shard is one lock and one LRU list. head.next is the most recently used entry, tail.prev the least.
type shard struct {
	m          map[string]*entry
	head, tail entry
	mu         sync.Mutex
	max        int
}

func newShard(maxEntries int) *shard {
	shd := &shard{m: make(map[string]*entry), max: maxEntries}
	shd.head.next, shd.tail.prev = &shd.tail, &shd.head

	return shd
}

func (ent *entry) unlink() {
	ent.prev.next, ent.next.prev = ent.next, ent.prev
}

func (shd *shard) pushFront(ent *entry) {
	ent.prev, ent.next = &shd.head, shd.head.next
	shd.head.next.prev = ent
	shd.head.next = ent
}

// cache is a bounded LRU of the answers of one segment, with a TTL for members and one for non members.
type cache struct {
	shards      []*shard
	seed        maphash.Seed
	ttl         time.Duration
	negativeTTL time.Duration
}

func newCache(opts SegmentOptions) *cache {
	count := min(opts.Shards, opts.MaxEntries)
	perShard := (opts.MaxEntries + count - 1) / count
	cch := &cache{
		shards: make([]*shard, count), seed: maphash.MakeSeed(), ttl: opts.TTL, negativeTTL: opts.NegativeTTL,
	}

	for i := range cch.shards {
		cch.shards[i] = newShard(perShard)
	}

	return cch
}

func (c *cache) shardOf(key string) *shard {
	return c.shards[maphash.String(c.seed, key)%uint64(len(c.shards))]
}

// get returns the cached answer of key at the instant now. It does not allocate.
func (c *cache) get(key string, now time.Time) (segment.State, bool) {
	shd := c.shardOf(key)

	shd.mu.Lock()
	defer shd.mu.Unlock()

	ent := shd.m[key]
	if ent == nil {
		return segment.Unknown, false
	}

	if !now.Before(ent.expires) {
		ent.unlink()
		delete(shd.m, key)

		return segment.Unknown, false
	}

	ent.unlink()
	shd.pushFront(ent)

	return ent.state, true
}

// put caches a Member or a NotMember answer. It ignores any other state.
func (c *cache) put(key string, state segment.State, now time.Time) {
	if state != segment.Member && state != segment.NotMember {
		return // Unknown and anything else is never cached
	}

	ttl := c.negativeTTL
	if state == segment.Member {
		ttl = c.ttl
	}

	shd := c.shardOf(key)

	shd.mu.Lock()
	defer shd.mu.Unlock()

	if ent := shd.m[key]; ent != nil {
		ent.state, ent.expires = state, now.Add(ttl)
		ent.unlink()
		shd.pushFront(ent)

		return
	}

	if len(shd.m) >= shd.max {
		oldest := shd.tail.prev
		oldest.unlink()
		delete(shd.m, oldest.key)
	}

	ent := &entry{key: key, state: state, expires: now.Add(ttl)}
	shd.m[key] = ent
	shd.pushFront(ent)
}

func (c *cache) clear() {
	for _, shd := range c.shards {
		shd.mu.Lock()
		clear(shd.m)
		shd.head.next, shd.tail.prev = &shd.tail, &shd.head
		shd.mu.Unlock()
	}
}

func (c *cache) len() int {
	total := 0

	for _, shd := range c.shards {
		shd.mu.Lock()
		total += len(shd.m)
		shd.mu.Unlock()
	}

	return total
}
