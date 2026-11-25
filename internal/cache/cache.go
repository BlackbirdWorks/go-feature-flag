package cache

import (
	"github.com/blackbirdworks/semaphore/modules/core/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

// Cache is the interface to represent a cache in the system.
type Cache interface {
	// addFlag add a flag in the cache
	addFlag(key string, value *flag.InternalFlag)

	// getFlag get a specific flag by the flag key
	getFlag(key string) (*flag.InternalFlag, error)

	// Copy return a copy version of the cache.
	Copy() *InMemoryCache

	// All return the complete list of the flags, as deep copies the caller is free to change.
	All() map[string]flag.Flag

	// shared returns the complete list of the flags without copying them: they are read-only.
	shared() map[string]flag.Flag

	// Init allow to initialize the cache with a collection of flags.
	Init(flags map[string]dto.DTO)
}
