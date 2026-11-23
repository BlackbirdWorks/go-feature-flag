package cache

import (
	"fmt"
	"log/slog"

	"github.com/thomaspoignant/go-feature-flag/modules/core/dto"
	"github.com/thomaspoignant/go-feature-flag/modules/core/flag"
	"github.com/thomaspoignant/go-feature-flag/utils/fflog"
)

var _ Cache = (*InMemoryCache)(nil)

type InMemoryCache struct {
	// Flags are the flags by key. A flag of the map is shared by every reader: it must be treated
	// as read-only once it is in the map.
	Flags  map[string]*flag.InternalFlag
	Logger *fflog.FFLogger
}

func NewInMemoryCache(logger *fflog.FFLogger) *InMemoryCache {
	return &InMemoryCache{
		Flags:  map[string]*flag.InternalFlag{},
		Logger: logger,
	}
}

func (fc *InMemoryCache) addFlag(key string, value flag.InternalFlag) {
	if err := value.IsValid(); err == nil {
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
		return &flag.InternalFlag{}, fmt.Errorf("flag [%v] does not exists", key)
	}
	return f, nil
}

func (fc *InMemoryCache) Copy() Cache {
	inMemoryCache := NewInMemoryCache(fc.Logger)
	for k, v := range fc.Flags {
		inMemoryCache.addFlag(k, *v)
	}
	return inMemoryCache
}

// All returns a snapshot of the flags. Every flag is a shallow copy of the cached one (the same
// semantics as before the lock-free lookup): the caller can keep the map or change its flags
// without affecting the cache. getFlag is the only accessor returning the shared flag.
func (fc *InMemoryCache) All() map[string]flag.Flag {
	c := make(map[string]flag.Flag, len(fc.Flags))
	for key, val := range fc.Flags {
		cp := *val
		c[key] = &cp
	}
	return c
}

func (fc *InMemoryCache) Init(flags map[string]dto.DTO) {
	cache := make(map[string]*flag.InternalFlag, len(flags))
	for key, flagDto := range flags {
		flagToAdd := flagDto.Convert()
		if err := flagToAdd.IsValid(); err == nil {
			cache[key] = &flagToAdd
		} else {
			fc.Logger.Error("[cache] invalid configuration for flag",
				slog.String("key", key), slog.Any("error", err.Error()))
		}
	}
	fc.Flags = cache
}
