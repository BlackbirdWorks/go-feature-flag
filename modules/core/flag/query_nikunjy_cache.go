package flag

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/blackbirdworks/semaphore/modules/core/utils"
	"github.com/blackbirdworks/semaphore/rules/parser"
)

const (
	// Rotation and shift distances of the query hash (xxhash like mixing).
	rotate0     = 29
	rotate1     = 31
	rotate2     = 33
	rotate3     = 37
	mixRotate1  = 11
	mixRotate2  = 23
	mixRotate3  = 47
	tailRotate  = 13
	finalShift1 = 32
	finalShift2 = 29
)

// nikunjyEvaluatorCache keeps one parsed *parser.Evaluator per query string. Parsing the
// query via ANTLR is the dominant cost of a nikunjy rule evaluation. An evaluator compiles
// its rule on the first Process and is then safe for concurrent use, so one evaluator per
// query serves every evaluation, and the garbage collector never drops it while the query
// is cached.
//
// An entry is evicted once no validated rule list contains its query. refs counts those
// rule lists per query: the cache is owned by a Service, which is shared by the flag
// sets that the same client reloads, so one reload must not evict a query another
// rule list still uses.
//
// Finding the entry of a query costs a hash of the whole query string, which is milliseconds
// for the megabytes of a large list, plus a comparison of its bytes. So the evaluation of a
// rule does not look its query up in entries. When a rule is validated, its query is hashed
// once and the hash is saved in the rule (Rule.QueryHash); byKey maps that hash to the
// evaluator, and evaluating the rule only reads byKey (see lookup).
type nikunjyEvaluatorCache struct {
	entries sync.Map // map[string]*parser.Evaluator
	refsMu  sync.Mutex
	refs    map[string]int

	// hash is the hash saved in the rules. It is a field so that tests can force collisions.
	hash func(string) uint64
	// byKey maps a rule's QueryHash to the evaluator of the rule's query text. A key is
	// shared by the rules that have the same text, and is never shared by two different
	// texts: intern gives the second text of a hash another key.
	byKey   sync.Map // map[uint64]*nikunjyKeyedEvaluator
	keysMu  sync.Mutex
	keyRefs map[uint64]int // the validated rule lists that use each key, protected by keysMu

	// keysEnabled is whether index saves keys. It is not when the keys could not be released: the
	// rules are then evaluated through get, as rules that are not validated. It is a field so that
	// tests can run that case.
	keysEnabled bool
	// epoch counts the resets. The cleanups of the rule lists that were retained before a reset must not
	// release anything from what was rebuilt after it, so each one remembers the epoch it was registered in.
	epoch atomic.Uint64
	// keysMinQueryLen is the length from which a query is given a key. A short query is hashed and
	// compared by get faster than a key is saved, released and looked up: giving it a key would only
	// make loading slower and keep its evaluator in memory. It is a field so that tests can give
	// keys to short queries.
	keysMinQueryLen int
}

// defaultNikunjyKeysMinQueryLen is the default length from which a nikunjy query is given a key.
const defaultNikunjyKeysMinQueryLen = 1024

// nikunjyKeyedEvaluator is the evaluator of a query text. Its key is only used for rules
// whose query is that text: lookup compares the text.
type nikunjyKeyedEvaluator struct {
	query     string
	evaluator *parser.Evaluator
}

// nikunjyRuleQuery is the query of rule number rule of the retained rules.
type nikunjyRuleQuery struct {
	rule    int
	raw     string
	trimmed string
	// hash is the hash of raw if known computed it already.
	hash   uint64
	hashed bool
}

// nikunjyKnownQuery is what known found for the query of a rule that qualifies for a key.
type nikunjyKnownQuery struct {
	// evaluator is the evaluator of a query whose text has a key already: the rule is validated
	// by it, with no lookup of its text in the evaluator cache.
	evaluator *parser.Evaluator
	// hash is the hash of the text of a query that has no key yet.
	hash uint64
}

// hashNikunjyQuery is a fast, non-cryptographic hash of the bytes of the query, that reads
// them 32 at a time. It does not rely on the runtime (no hash/maphash) and it has no seed, so
// the hash of a query is the same in every process. Its collisions are handled by intern.
func hashNikunjyQuery(query string) uint64 {
	const (
		prime1 = 0x9E3779B185EBCA87
		prime2 = 0xC2B2AE3D27D4EB4F
		prime3 = 0x165667B19E3779F9
	)
	b := unsafe.Slice(
		unsafe.StringData(query),
		len(query),
	)
	h0, h1, h2, h3 := uint64(len(query))*prime3, uint64(prime1), uint64(prime2), uint64(prime3)
	for ; len(b) >= 32; b = b[32:] {
		h0 = bits.RotateLeft64((h0^binary.LittleEndian.Uint64(b))*prime1, rotate0)
		h1 = bits.RotateLeft64((h1^binary.LittleEndian.Uint64(b[8:]))*prime2, rotate1)
		h2 = bits.RotateLeft64((h2^binary.LittleEndian.Uint64(b[16:]))*prime3, rotate2)
		h3 = bits.RotateLeft64((h3^binary.LittleEndian.Uint64(b[24:]))*prime1, rotate3)
	}
	h := h0 ^ bits.RotateLeft64(h1, mixRotate1) ^ bits.RotateLeft64(h2, mixRotate2) ^ bits.RotateLeft64(h3, mixRotate3)
	for ; len(b) >= 8; b = b[8:] {
		h = bits.RotateLeft64((h^binary.LittleEndian.Uint64(b))*prime2, rotate1)
	}
	for _, c := range b {
		h = bits.RotateLeft64((h^uint64(c))*prime1, tailRotate)
	}
	h ^= h >> finalShift1
	h *= prime2
	h ^= h >> finalShift2

	return h
}

func newNikunjyEvaluatorCache() *nikunjyEvaluatorCache {
	return &nikunjyEvaluatorCache{
		refs:    map[string]int{},
		hash:    hashNikunjyQuery,
		keyRefs: map[uint64]int{},

		keysEnabled:     true,
		keysMinQueryLen: defaultNikunjyKeysMinQueryLen,
	}
}

func (c *nikunjyEvaluatorCache) get(query string) (*parser.Evaluator, error) {
	if v, ok := c.entries.Load(query); ok {
		return v.(*parser.Evaluator), nil //nolint:errcheck // the maps only ever store this type
	}
	ev, err := parser.NewEvaluator(query)
	if err != nil {
		return nil, err
	}
	actual, _ := c.entries.LoadOrStore(query, ev)

	return actual.(*parser.Evaluator), nil //nolint:errcheck // the maps only ever store this type
}

// validate parses the query through the cache and evaluates it on an empty context. That
// first Process also compiles the rule, so its first evaluation does not pay for it.
func (c *nikunjyEvaluatorCache) validate(query string) error {
	_, err := c.validateEvaluator(query)

	return err
}

// validateEvaluator is validate. It also returns the evaluator of the query.
func (c *nikunjyEvaluatorCache) validateEvaluator(query string) (*parser.Evaluator, error) {
	ev, err := c.get(query)
	if err != nil {
		return nil, fmt.Errorf("invalid query: %w", err)
	}
	if _, processErr := ev.Process(map[string]any{}); processErr != nil {
		return nil, fmt.Errorf("invalid query: %w", processErr)
	}

	return ev, nil
}

// retain keeps the entries of the nikunjy queries in rules until rules is garbage
// collected. After a configuration reload, the rules of the previous configuration
// become unreachable, and the queries that no remaining rule list uses are evicted.
// The queries that known found are not retained: their keys keep them.
//
// It returns the queries of the rules, for index.
func (c *nikunjyEvaluatorCache) retain(rules *[]Rule, known []nikunjyKnownQuery) []nikunjyRuleQuery {
	if rules == nil {
		return nil
	}
	// The epoch is read before anything is counted: a reset in between makes the cleanup a no-op (the
	// entries are kept), never an eviction of what the rebuilt cache holds.
	epoch := c.epoch.Load()
	queries := make([]string, 0, len(*rules))
	ruleQueries := make([]nikunjyRuleQuery, 0, len(*rules))
	for i := range *rules {
		if known != nil && known[i].evaluator != nil {
			continue
		}
		raw := (*rules)[i].GetQuery()
		trimmed := utils.StrTrim(raw)
		if queryFormatOf(trimmed) != NikunjyQueryFormat {
			continue
		}
		queries = append(queries, trimmed)
		if trimmed != "" {
			q := nikunjyRuleQuery{rule: i, raw: raw, trimmed: trimmed}
			if known != nil && known[i].hash != 0 {
				q.hash, q.hashed = known[i].hash, true
			}
			ruleQueries = append(ruleQueries, q)
		}
	}
	if len(queries) == 0 {
		return nil
	}
	c.refsMu.Lock()
	for _, q := range queries {
		c.refs[q]++
	}
	c.refsMu.Unlock()
	runtime.AddCleanup(rules, func(q []string) { c.releaseAt(epoch, q) }, queries)

	return ruleQueries
}

// mayBeNikunjyQuery is false for a query that is surely not a nikunjy query, JsonLogic being
// the other format. It reads the start of the query only.
func mayBeNikunjyQuery(query string) bool {
	trimmed := strings.TrimLeft(query, " \t\n\r")

	return !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "null")
}

// known looks up the queries of the enabled rules that qualify for a key by their hash, before
// the rules are validated. A query whose text has a key already was validated, and its evaluator
// is kept: the rule is then validated without looking its text up in the evaluator cache, and
// the text is read once, to hash it and to compare it with the text of the key, as it is for the
// queries of the previous load. The keys it finds are released once rules is garbage collected.
// For the other queries it saves the hash, for index. It returns nil if no query qualifies.
func (c *nikunjyEvaluatorCache) known(rules *[]Rule) []nikunjyKnownQuery {
	if rules == nil || !c.keysEnabled {
		return nil
	}
	epoch := c.epoch.Load() // before the references are counted, see retain
	var found []nikunjyKnownQuery
	var keys []uint64
	for i := range *rules {
		r := &(*rules)[i]
		if !c.qualifiesForKnown(r) {
			continue
		}
		if found == nil {
			found = make([]nikunjyKnownQuery, len(*rules))
		}
		key, entry, hash := c.knownEntry(r)
		if entry == nil {
			found[i].hash = hash

			continue
		}
		found[i].evaluator = entry.evaluator
		keys = append(keys, key)
		// The rule shares the bytes of the text of its key: see index.
		if unsafe.StringData(
			*r.Query,
		) != unsafe.StringData(
			entry.query,
		) {
			*r.Query = entry.query
		}
	}
	if len(keys) > 0 {
		runtime.AddCleanup(rules, func(k []uint64) { c.unindexAt(epoch, k) }, keys)
	}

	return found
}

// qualifiesForKnown is true for an enabled rule whose query may be a nikunjy query long enough
// to have a key.
func (c *nikunjyEvaluatorCache) qualifiesForKnown(r *Rule) bool {
	return !r.IsDisable() && r.Query != nil && len(*r.Query) >= c.keysMinQueryLen &&
		mayBeNikunjyQuery(*r.Query)
}

// knownEntry finds the entry of the query of r. A rule that was validated before has its key and
// text already. Otherwise the query is hashed, the hash is returned so that index does not hash
// it again, and the key is saved in r if the query has one. It returns a nil entry if the query
// has no key.
func (c *nikunjyEvaluatorCache) knownEntry(r *Rule) (uint64, *nikunjyKeyedEvaluator, uint64) {
	raw := *r.Query
	key := r.QueryHash
	if entry := c.referenceEntry(key, raw); entry != nil {
		return key, entry, 0
	}
	hash := c.hash(raw)
	key, entry := c.find(hash, raw)
	if entry != nil {
		r.QueryHash = key
	}

	return key, entry, hash
}

// index saves the hash of the queries in their rules, so that evaluating a rule finds its
// evaluator without hashing the query. It must run once the queries are validated, and only for
// the queries that retain returned for rules; evaluators[i] is the evaluator that validated the
// query of rules[i]. The keys it adds are removed once rules is garbage collected. It is the only
// code that writes QueryHash and the Query of a rule (with known), so rules must not be
// evaluated while they are indexed for the first time: a configuration is validated before it is
// served.
func (c *nikunjyEvaluatorCache) index(rules *[]Rule, queries []nikunjyRuleQuery, evaluators []*parser.Evaluator) {
	if !c.keysEnabled {
		return
	}
	epoch := c.epoch.Load() // before the references are counted, see retain
	keys := make([]uint64, 0, len(queries))
	for _, q := range queries {
		ev := evaluators[q.rule]
		r := &(*rules)[q.rule]
		if ev == nil || len(q.raw) < c.keysMinQueryLen || r.Query == nil || *r.Query != q.raw {
			continue // not validated, short, or changed while the rules were validated
		}
		hash := q.hash
		if !q.hashed {
			hash = c.hash(q.raw)
		}
		key, entry := c.intern(q.raw, hash, ev)
		keys = append(keys, key)
		r.QueryHash = key
		// The rule shares the bytes of the text of its key. The text is the same, but the
		// evaluation of the rule recognizes it without reading its bytes, and the memory of the
		// copies that every flag holds is released.
		if unsafe.StringData(
			q.raw,
		) != unsafe.StringData(
			entry.query,
		) {
			*r.Query = entry.query
		}
	}
	if len(keys) > 0 {
		runtime.AddCleanup(rules, func(k []uint64) { c.unindexAt(epoch, k) }, keys)
	}
}

// referenceEntry counts a reference to key and returns its entry if it is the key of the text
// raw, and returns nil otherwise.
func (c *nikunjyEvaluatorCache) referenceEntry(key uint64, raw string) *nikunjyKeyedEvaluator {
	if key == 0 {
		return nil
	}
	c.keysMu.Lock()
	defer c.keysMu.Unlock()
	v, ok := c.byKey.Load(key)
	if !ok || v.(*nikunjyKeyedEvaluator).query != raw { //nolint:errcheck // the maps only ever store this type
		return nil
	}
	c.keyRefs[key]++

	return v.(*nikunjyKeyedEvaluator) //nolint:errcheck // the maps only ever store this type
}

// find counts a reference to the entry of the text raw, whose hash is hash, and returns its key
// and the entry. It returns a nil entry if there is none. The keys are probed as intern does.
func (c *nikunjyEvaluatorCache) find(hash uint64, raw string) (uint64, *nikunjyKeyedEvaluator) {
	c.keysMu.Lock()
	defer c.keysMu.Unlock()
	for key := hash; ; key++ {
		if key == 0 { // 0 is the key of rules that are not validated
			continue
		}
		v, ok := c.byKey.Load(key)
		if !ok {
			return 0, nil
		}
		//nolint:errcheck // the maps only ever store this type
		if entry := v.(*nikunjyKeyedEvaluator); entry.query == raw {
			c.keyRefs[key]++

			return key, entry
		}
	}
}

// intern returns the key and the entry of the query text raw, whose hash is hash, and counts a
// reference to them. The key is the hash of raw, unless another text has it: the keys are then
// probed linearly until one is free or has the text raw. So a key never leads to the evaluator of
// another text. Texts are compared in full, once for each rule that is validated, never while
// evaluating. ev, the evaluator of raw, is the one of the entry if there is none yet.
func (c *nikunjyEvaluatorCache) intern(raw string, hash uint64, ev *parser.Evaluator) (uint64, *nikunjyKeyedEvaluator) {
	c.keysMu.Lock()
	defer c.keysMu.Unlock()
	for key := hash; ; key++ {
		if key == 0 { // 0 is the key of rules that are not validated
			continue
		}
		v, ok := c.byKey.Load(key)
		if !ok {
			entry := &nikunjyKeyedEvaluator{query: raw, evaluator: ev}
			c.byKey.Store(key, entry)
			c.keyRefs[key]++

			return key, entry
		}
		//nolint:errcheck // the maps only ever store this type
		if entry := v.(*nikunjyKeyedEvaluator); entry.query == raw {
			c.keyRefs[key]++

			return key, entry
		}
	}
}

// unindexAt releases the keys of a collected rule list that was indexed in epoch (it does nothing once the
// cache was reset). A rule that still has a key that was removed falls back to get, which is slower but
// returns the same evaluator.
func (c *nikunjyEvaluatorCache) unindexAt(epoch uint64, keys []uint64) {
	c.keysMu.Lock()
	defer c.keysMu.Unlock()
	if c.epoch.Load() != epoch {
		return
	}
	for _, key := range keys {
		c.keyRefs[key]--
		if c.keyRefs[key] <= 0 {
			delete(c.keyRefs, key)
			c.byKey.Delete(key)
		}
	}
}

// lookup returns the evaluator of a validated rule from the key saved in it, or nil when
// the rule has no key or the key does not lead to its query, in which case the caller uses
// get. It compares raw, the query of the rule, with the text that the key was given for:
// so a rule whose query was changed after its validation, or whose key was copied or
// guessed wrong, never gets the evaluator of another query. The comparison is cheap
// when the texts share their bytes, which they do for the rules that were validated (index
// makes them share), as the runtime sees that the two strings are the same memory before it reads them.
func (c *nikunjyEvaluatorCache) lookup(key uint64, raw string) *parser.Evaluator {
	if key == 0 {
		return nil
	}
	v, ok := c.byKey.Load(key)
	if !ok {
		return nil
	}
	e := v.(*nikunjyKeyedEvaluator) //nolint:errcheck // the maps only ever store this type
	if e.query != raw {
		return nil
	}

	return e.evaluator
}

// releaseAt releases the queries of a collected rule list that was retained in epoch (it does nothing once the
// cache was reset).
func (c *nikunjyEvaluatorCache) releaseAt(epoch uint64, queries []string) {
	c.refsMu.Lock()
	defer c.refsMu.Unlock()
	if c.epoch.Load() != epoch {
		return
	}
	for _, q := range queries {
		c.refs[q]--
		if c.refs[q] <= 0 {
			delete(c.refs, q)
			c.entries.Delete(q)
		}
	}
}

// reset drops every evaluator and key. The rule lists that were validated keep working: a key that
// no longer leads to an evaluator falls back to get, and the reference counts restart from zero. The
// cleanups of the rule lists retained before the reset are ignored (see epoch), so they cannot evict what
// the rule lists validated after it retain.
func (c *nikunjyEvaluatorCache) reset() {
	c.refsMu.Lock()
	c.keysMu.Lock()
	c.epoch.Add(1)
	c.keysMu.Unlock()
	c.refsMu.Unlock()
	c.entries.Clear()
	c.byKey.Clear()
	c.refsMu.Lock()
	clear(c.refs)
	c.refsMu.Unlock()
	c.keysMu.Lock()
	clear(c.keyRefs)
	c.keysMu.Unlock()
}
