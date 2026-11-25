package service

// Whitebox: flagsetManagerImpl, newFlagsetManagerWithFlagsetsAndRegistry and addFlagset are
// unexported, and these tests inject a fake shared bucket registry. The external tests of the
// manager are in flagset_manager_test.go.

import (
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/testhelpers/slogutil"
)

const (
	flags                               = "flags"
	flagset3                            = "flagset"
	pBYaml                              = "p/b.yaml"
	pCYaml                              = "p/c.yaml"
	testdataSemaphoreConfigurationFlags = "../testdata/semaphore/configuration_flags.yaml"
)

func Test_flagsetManagerImpl_sharedPollerPerBucket(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		testSharedPollerManager(t)
	})
}

func testSharedPollerManager(t *testing.T) {
	t.Helper()

	content, err := os.ReadFile(testdataSemaphoreConfigurationFlags)
	require.NoError(t, err)

	fake := &fakeS3{content: content}

	var created atomic.Int32

	reg := newFakeRegistry(t, fake, &created)
	nonShared := config.FlagSet{
		Name:    "isolated",
		APIKeys: []string{"isolated-key"},
		Retriever: &retrieverconf.RetrieverConf{
			Kind: retrieverconf.FileRetriever,
			Path: testdataSemaphoreConfigurationFlags,
		},
	}
	cfg := &config.Config{FlagSets: []config.FlagSet{
		s3FlagSet("u1", flags, pAYaml),
		s3FlagSet("u2", flags, pBYaml),
		nonShared,
	}}
	mgr, err := newFlagsetManagerWithFlagsetsAndRegistry(cfg, slog.New(slog.DiscardHandler), nil, nil, nil, reg)
	require.NoError(t, err)

	// two shared-poller flagsets on the same bucket share one group, the non shared-poller one does not use any
	assert.EqualValues(t, 1, created.Load())
	assert.Equal(t, 1, reg.groupCount())
	// all the shared-poller items are pre-registered, so startup lists the directory once
	assert.EqualValues(t, 1, fake.lists.Load())
	assert.EqualValues(t, 2, fake.gets.Load())

	// a flagset added at runtime joins the same group
	mgr.addFlagset(s3FlagSet("u3", flags, pCYaml), 0)
	assert.EqualValues(t, 1, created.Load())
	assert.Equal(t, 1, reg.groupCount())

	all, err := mgr.AllFlagSets()
	require.NoError(t, err)
	assert.Contains(t, all, "u3")

	// removing flagsets releases the group once the last one is gone
	mgr.removeFlagset(s3FlagSet("u1", flags, pAYaml))
	mgr.removeFlagset(s3FlagSet("u2", flags, pBYaml))
	assert.Equal(t, 1, reg.groupCount())
	mgr.Close()
	assert.Equal(t, 0, reg.groupCount())
}

func Test_flagsetManagerImpl_addFlagset_sharedDuplicate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		content, err := os.ReadFile(testdataSemaphoreConfigurationFlags)
		require.NoError(t, err)
		reg := newFakeRegistry(t, &fakeS3{content: content}, &atomic.Int32{})
		first := s3FlagSet("u1", flags, pAYaml)
		mgr, err := newFlagsetManagerWithFlagsetsAndRegistry(
			&config.Config{FlagSets: []config.FlagSet{first}}, slog.New(slog.DiscardHandler), nil, nil, nil, reg)
		require.NoError(t, err)

		// the flagset name is already in the configuration: the new client is closed and released
		dup := s3FlagSet("u1", flags, pBYaml)
		mgr.addFlagset(dup, 0)
		assert.Equal(t, 1, reg.groupCount())
		mgr.Close()
		assert.Equal(t, 0, reg.groupCount())
	})
}

func Test_flagsetManagerImpl_reloadAddsSharedPollerFlagsets(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		content, err := os.ReadFile(testdataSemaphoreConfigurationFlags)
		require.NoError(t, err)

		fake := &fakeS3{content: content}

		flagsets := make([]config.FlagSet, 11)
		for i := range flagsets {
			fake.keys = append(fake.keys, fmt.Sprintf("p/f%d.yaml", i))
			flagsets[i] = s3FlagSet(fmt.Sprintf("u%d", i), flags, fake.keys[i])
		}

		reg := newFakeRegistry(t, fake, &atomic.Int32{})
		mgr, err := newFlagsetManagerWithFlagsetsAndRegistry(
			&config.Config{FlagSets: flagsets[:1]}, slog.New(slog.DiscardHandler), nil, nil, nil, reg)
		require.NoError(t, err)
		require.EqualValues(t, 1, fake.lists.Load())

		// 10 flagsets added in one reload share one listing
		mgr.OnConfigChange(&config.Config{Server: config.Server{Port: 8080}, FlagSets: flagsets})
		all, err := mgr.AllFlagSets()
		require.NoError(t, err)
		assert.Len(t, all, 11)
		assert.EqualValues(t, 2, fake.lists.Load())
		mgr.Close()
		assert.Equal(t, 0, reg.groupCount())
	})
}

func newPollingManager(
	t *testing.T, fake *fakeS3, interval time.Duration, logger *slog.Logger,
) (*FlagsetManagerImpl, *sharedBucketRegistry) {
	t.Helper()

	content, err := os.ReadFile(testdataSemaphoreConfigurationFlags)
	require.NoError(t, err)

	fake.content = content
	reg := newFakeRegistryWithPolling(t, fake, &atomic.Int32{}, interval, false, logger)
	flagsetA, flagsetB := s3FlagSet("u1", flags, pAYaml), s3FlagSet("u2", flags, pBYaml)
	flagsetA.PollingInterval, flagsetB.PollingInterval = 0, 0
	mgr, err := newFlagsetManagerWithFlagsetsAndRegistry(
		&config.Config{FlagSets: []config.FlagSet{flagsetA, flagsetB}}, logger, nil, nil, nil, reg)
	require.NoError(t, err)

	return mgr, reg
}

func Test_flagsetManagerImpl_sharedPoller(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := &fakeS3{}
		mgr, reg := newPollingManager(t, fake, 10*time.Second, slog.New(slog.DiscardHandler))
		require.EqualValues(t, 1, fake.lists.Load())
		require.EqualValues(t, 2, fake.gets.Load())

		// the clients do not poll, one listing per polling interval of the relay proxy
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		assert.EqualValues(t, 1+12, fake.lists.Load())
		assert.EqualValues(t, 2, fake.gets.Load(), "nothing changed")

		// only the flagset of the changed object downloads it
		fake.setETag(pAYaml, `"y"`)
		time.Sleep(10 * time.Second)
		synctest.Wait()
		assert.EqualValues(t, 1+13, fake.lists.Load())
		assert.Equal(t, 2, fake.getCount(pAYaml))
		assert.Equal(t, 1, fake.getCount(pBYaml))

		mgr.Close()
		assert.Equal(t, 0, reg.groupCount())
	})
}

func Test_flagsetManagerImpl_OnConfigChange_sharedPollerPolling(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		core, logs := slogutil.NewObserver(slog.LevelInfo)
		fake := &fakeS3{}
		mgr, reg := newPollingManager(t, fake, 10*time.Second, core)
		// a new top-level pollingInterval is applied to the running poller, the flagsets are not rebuilt
		flagsetA, flagsetB := s3FlagSet("u1", flags, pAYaml), s3FlagSet("u2", flags, pBYaml)
		flagsetA.PollingInterval, flagsetB.PollingInterval = 0, 0
		flagsetB.PollingInterval = 20000
		added := s3FlagSet("u3", flags, pCYaml)
		added.PollingInterval = 20000
		before, err := mgr.AllFlagSets()
		require.NoError(t, err)

		mgr.config = &config.Config{FlagSets: []config.FlagSet{flagsetA, flagsetB}}
		mgr.OnConfigChange(&config.Config{
			Server:          config.Server{Port: 8080},
			PollingInterval: 30000,
			FlagSets:        []config.FlagSet{flagsetA, flagsetB, added},
		})
		after, err := mgr.AllFlagSets()
		require.NoError(t, err)
		assert.Same(t, before["u1"], after["u1"])

		lists := fake.lists.Load() // the added flagset did its own initial listing

		time.Sleep(29 * time.Second)
		synctest.Wait()
		assert.Equal(t, lists, fake.lists.Load())
		time.Sleep(time.Second)
		synctest.Wait()
		assert.Equal(t, lists+1, fake.lists.Load())

		assert.Equal(t, 1, logs.FilterMessageSnippet("polling of the shared pollers updated").Len())
		// every flagset (the existing one and the one added by the reload) is warned once
		warns := logs.FilterMessageSnippet("the pollingInterval of the flagset is ignored")
		require.Equal(t, 2, warns.Len())
		warned := []any{warns.All()[0].ContextMap()[flagset3], warns.All()[1].ContextMap()[flagset3]}
		assert.ElementsMatch(t, []any{"u2", "u3"}, warned)

		// the same polling again changes nothing
		mgr.OnConfigChange(&config.Config{
			Server:          config.Server{Port: 8080},
			PollingInterval: 30000,
			FlagSets:        []config.FlagSet{flagsetA, flagsetB, added},
		})
		assert.Equal(t, 1, logs.FilterMessageSnippet("polling of the shared pollers updated").Len())
		mgr.Close()
		assert.Equal(t, 0, reg.groupCount())
	})
}

func Test_flagsetManagerImpl_reloadAppliesCleanQueueIntervalToAddedFlagsets(t *testing.T) {
	t.Parallel()

	retriever := &retrieverconf.RetrieverConf{Kind: file2, Path: testdataSemaphoreConfigurationFlags}

	tests := []struct {
		name          string
		startupTop    time.Duration
		reloadTop     time.Duration
		addedOverride time.Duration
		want          time.Duration
	}{
		{
			name:       "added flagset follows the reloaded top-level value",
			startupTop: time.Minute,
			reloadTop:  7 * time.Second,
			want:       7 * time.Second,
		},
		{
			name:          "explicit flagset value wins",
			startupTop:    time.Minute,
			reloadTop:     7 * time.Second,
			addedOverride: 3 * time.Second,
			want:          3 * time.Second,
		},
		{name: "reload clears the top-level value", startupTop: time.Minute, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			existing := config.FlagSet{Name: "existing", APIKeys: []string{"k1"}, Retriever: retriever}
			cfg := &config.Config{
				FlagSets:                   []config.FlagSet{existing},
				ExporterCleanQueueInterval: tt.startupTop,
			}
			mgr, err := NewFlagsetManager(cfg, slog.New(slog.DiscardHandler), nil, nil, nil)
			require.NoError(t, err)
			t.Cleanup(mgr.Close)

			var got, calls atomic.Int64

			mgr.clientOptions = []clientOption{
				withClientFactory(func(clientCfg ffclient.Config) (*ffclient.GoFeatureFlag, error) {
					calls.Add(1)
					got.Store(int64(clientCfg.ExporterCleanQueueInterval))

					return ffclient.New(clientCfg)
				}),
			}

			added := config.FlagSet{
				Name: "added", APIKeys: []string{"k2"},
				Retriever: retriever, ExporterCleanQueueInterval: tt.addedOverride,
			}
			mgr.onConfigChangeWithFlagsets(&config.Config{
				FlagSets:                   []config.FlagSet{existing, added},
				ExporterCleanQueueInterval: tt.reloadTop,
			})
			assert.Equal(t, int64(1), calls.Load(), "the added flagset must be built exactly once")
			assert.Equal(t, tt.want, time.Duration(got.Load()))

			client, err := mgr.FlagSet("k2")
			require.NoError(t, err)
			assert.NotNil(t, client)
		})
	}
}
