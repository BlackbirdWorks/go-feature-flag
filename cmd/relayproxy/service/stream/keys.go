package stream

import (
	"hash/maphash"
	"iter"
)

// KeyRoutes lists the API keys that route to a flag set now, with the name of the flag set. It is a sequence, so that
// a service that has no client with a key never reads it.
type KeyRoutes = iter.Seq2[string, string]

// keyHasher makes the identifier a client keeps of its API key: a keyed hash with a seed that is random for the
// process, so the identifier says nothing of the key outside this process and can not be turned back into it. The key
// itself is never kept, logged or shown.
type keyHasher struct{ seed maphash.Seed }

func newKeyHasher() keyHasher { return keyHasher{seed: maphash.MakeSeed()} }

func (h keyHasher) id(key string) uint64 { return maphash.String(h.seed, key) }

// liveIDs maps the identifier of every key that routes now to its flag set.
func (h keyHasher) liveIDs(routes KeyRoutes) map[uint64]string {
	live := map[uint64]string{}

	for key, set := range routes {
		live[h.id(key)] = set
	}

	return live
}
