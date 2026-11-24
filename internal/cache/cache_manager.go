package cache

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/internal/notification"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/modules/core/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

var (
	errImpossibleToReadThe = errors.New("impossible to read the flag before the initialisation")
)

var _ Manager = (*cacheManagerImpl)(nil)

type Manager interface {
	UpdateCache(newFlags map[string]dto.DTO, log *fflog.FFLogger, notifyChanges bool) error
	Close()
	GetFlag(key string) (flag.Flag, error)
	// AllFlags returns a snapshot of the flags that the caller is free to change.
	AllFlags() (map[string]flag.Flag, error)
	// AllFlagsReadOnly returns the flags of the cache without copying them (cheap): the caller must only
	// read them, and must not keep them or hand them to code that does not.
	AllFlagsReadOnly() (map[string]flag.Flag, error)
	GetLatestUpdateDate() time.Time
}

//nolint:govet // fieldalignment: the original order is 3-5% faster on GetFlag (cache-line layout, measured)
type cacheManagerImpl struct {
	// inMemoryCache is the current flag cache: it is replaced as a whole by UpdateCache, so the
	// evaluations read it without taking any lock (nil when the manager is closed).
	inMemoryCache atomic.Pointer[Cache]
	// mutex protects latestUpdate and serializes the writers of inMemoryCache.
	mutex                           sync.RWMutex
	notificationService             notification.Service
	latestUpdate                    time.Time
	logger                          *fflog.FFLogger
	persistentFlagConfigurationFile string
	// persistWg tracks the in-flight PersistCache goroutines.
	persistWg sync.WaitGroup
	// service validates the flags that are loaded, and owns the parses of their queries.
	service *flag.Service
}

// Option customizes the Manager that New creates.
type Option func(*cacheManagerImpl)

// WithService makes the manager validate its flags with svc, which the caller owns (and closes).
// By default the manager creates a Service of its own.
func WithService(svc *flag.Service) Option {
	return func(m *cacheManagerImpl) { m.service = svc }
}

func New(
	notificationService notification.Service,
	persistentFlagConfigurationFile string,
	logger *fflog.FFLogger,
	opts ...Option,
) Manager {
	m := &cacheManagerImpl{
		logger:                          logger,
		mutex:                           sync.RWMutex{},
		notificationService:             notificationService,
		persistentFlagConfigurationFile: persistentFlagConfigurationFile,
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.service == nil {
		m.service = flag.NewService()
	}
	var initial Cache = NewInMemoryCache(m.service, logger)
	m.inMemoryCache.Store(&initial)

	return m
}

func ConvertToFlagStruct(
	loadedFlags []byte,
	fileFormat string,
) (map[string]dto.DTO, error) {
	var newFlags map[string]dto.DTO
	var err error
	switch strings.ToLower(fileFormat) {
	case "toml":
		err = toml.Unmarshal(loadedFlags, &newFlags)
	case "json":
		err = json.Unmarshal(loadedFlags, &newFlags)
	default:
		// default unmarshaller is YAML
		err = yaml.Unmarshal(loadedFlags, &newFlags)
	}

	return newFlags, err
}

func (c *cacheManagerImpl) UpdateCache(
	newFlags map[string]dto.DTO,
	log *fflog.FFLogger,
	notifyChanges bool,
) error {
	newCache := NewInMemoryCache(c.service, c.logger)
	newCache.Init(newFlags)
	newCacheFlags := newCache.All()
	oldCacheFlags := map[string]flag.Flag{}

	var swapped Cache = newCache
	c.mutex.Lock()
	old := c.inMemoryCache.Swap(&swapped)
	c.latestUpdate = clock.Now()
	c.mutex.Unlock()
	// collect flags for compare (the old cache is immutable, no lock needed).
	if old != nil {
		oldCacheFlags = (*old).All()
	}

	if notifyChanges {
		// notify the changes
		c.notificationService.Notify(oldCacheFlags, newCacheFlags, log)
	}
	// persist the cache on disk
	if c.persistentFlagConfigurationFile != "" {
		c.PersistCache(oldCacheFlags, newCacheFlags)
	}

	return nil
}

func (c *cacheManagerImpl) Close() {
	// Wait for the in-flight persistence goroutines: nothing must write the persistent flag
	// configuration file after Close() returned. This cannot deadlock nor race with a new
	// PersistCache because the retriever manager stops the polling before calling Close(),
	// so no new persistence can be started at this point.
	c.persistWg.Wait()

	// Clear the cache
	c.inMemoryCache.Store(nil)
	if c.notificationService != nil {
		c.notificationService.Close()
	}
}

func (c *cacheManagerImpl) GetFlag(key string) (flag.Flag, error) {
	current := c.inMemoryCache.Load()
	if current == nil {
		return nil, errImpossibleToReadThe
	}

	return (*current).getFlag(key)
}

func (c *cacheManagerImpl) AllFlags() (map[string]flag.Flag, error) {
	current := c.inMemoryCache.Load()
	if current == nil {
		return nil, errImpossibleToReadThe
	}

	return (*current).All(), nil
}

func (c *cacheManagerImpl) AllFlagsReadOnly() (map[string]flag.Flag, error) {
	current := c.inMemoryCache.Load()
	if current == nil {
		return nil, errImpossibleToReadThe
	}

	return (*current).shared(), nil
}

func (c *cacheManagerImpl) GetLatestUpdateDate() time.Time {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return c.latestUpdate
}

// PersistCache is writing the flags to a file to be able to restart without being able to access the retrievers.
// It is useful to have a fallback in case of a problem with the retrievers, such as a network issue.
//
// The persistence is done in a goroutine to not block the main thread.
func (c *cacheManagerImpl) PersistCache(oldCache, newCache map[string]flag.Flag) {
	c.persistWg.Go(func() {
		if _, err := os.Stat(c.persistentFlagConfigurationFile); !os.IsNotExist(err) &&
			cmp.Equal(oldCache, newCache) {
			c.logger.Debug("No change in the cache, skipping the persist")

			return
		}
		data, err := yaml.Marshal(newCache)
		if err != nil {
			c.logger.Error("Error while marshalling flags to persist", slog.Any("error", err.Error()))

			return
		}

		err = os.WriteFile(c.persistentFlagConfigurationFile, data, 0600)
		if err != nil {
			c.logger.Error("Error while writing flags to file", slog.Any("error", err.Error()))

			return
		}
		c.logger.Info(
			"Flags cache persisted to file",
			slog.String("file", c.persistentFlagConfigurationFile),
		)
	})
}
