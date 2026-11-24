package cache

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/blackbirdworks/semaphore/modules/core/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

var (
	errFlag = errors.New("flag")
)

var _ Cache = (*InMemoryCache)(nil)

type InMemoryCache struct {
	// Flags are the flags by key. A flag of the map is shared by every reader: it must be treated
	// as read-only once it is in the map.
	Flags  map[string]*flag.InternalFlag
	Logger *fflog.FFLogger
	// Service validates the flags that are added, and keeps the parses of their queries.
	Service *flag.Service
}

// NewInMemoryCache creates an empty cache, which validates its flags with svc.
func NewInMemoryCache(svc *flag.Service, logger *fflog.FFLogger) *InMemoryCache {
	return &InMemoryCache{
		Flags:   map[string]*flag.InternalFlag{},
		Logger:  logger,
		Service: svc,
	}
}

func (fc *InMemoryCache) addFlag(key string, value flag.InternalFlag) {
	if err := value.IsValid(fc.Service); err == nil {
		fc.Flags[key] = &value
	} else {
		fc.Logger.Error("[cache] invalid configuration for flag",
			slog.String("key", key), slog.Any("error", err.Error()))
	}
}

// getFlag returns the shared cached flag, without copying it, to keep the evaluation lookup
// allocation-free. The caller must not modify it.
func (fc *InMemoryCache) getFlag(key string) (flag.Flag, error) {
	f, ok := fc.Flags[key]
	if !ok {
		// callers expect a usable (empty) flag next to the error
		return &flag.InternalFlag{}, fmt.Errorf("%w [%v] does not exists", errFlag, key)
	}

	return f, nil
}

func (fc *InMemoryCache) Copy() Cache {
	inMemoryCache := NewInMemoryCache(fc.Service, fc.Logger)
	for k, v := range fc.Flags {
		inMemoryCache.addFlag(k, *v)
	}

	return inMemoryCache
}

// All returns a snapshot of the flags: every flag is a deep copy of the cached one, so the
// caller can keep the map and change its flags, at any depth, without affecting the cache.
// The copy is not free: the evaluations read the flags with getFlag or shared, which do not copy.
func (fc *InMemoryCache) All() map[string]flag.Flag {
	c := make(map[string]flag.Flag, len(fc.Flags))
	for key, val := range fc.Flags {
		c[key] = val.DeepCopy()
	}

	return c
}

// shared returns the flags of the cache without copying them. The caller must treat them as
// read-only and must not hand them to code that does not.
func (fc *InMemoryCache) shared() map[string]flag.Flag {
	c := make(map[string]flag.Flag, len(fc.Flags))
	for key, val := range fc.Flags {
		c[key] = val
	}

	return c
}

func (fc *InMemoryCache) Init(flags map[string]dto.DTO) {
	cache := make(map[string]*flag.InternalFlag, len(flags))
	for key, flagDto := range flags {
		flagToAdd := flagDto.Convert()
		if err := flagToAdd.IsValid(fc.Service); err == nil {
			cache[key] = &flagToAdd
		} else {
			fc.Logger.Error("[cache] invalid configuration for flag",
				slog.String("key", key), slog.Any("error", err.Error()))
		}
	}
	fc.Flags = cache
}
