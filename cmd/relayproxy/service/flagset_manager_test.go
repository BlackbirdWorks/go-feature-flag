package service_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/testhelpers/slogutil"
	"github.com/blackbirdworks/semaphore/utils"
)

const (
	apiKeyOne                           = "api-key-1"
	apiKeyTwo                           = "api-key-2"
	apiKeyMove                          = "api-key-move"
	brokenKey                           = "broken-key"
	expectedExactlyOneError             = "Expected exactly one error log message"
	failedToCreateFlagsetManager        = "failed to create FlagsetManager"
	flagsetB                            = "flagset-b"
	flagsetC                            = "flagset-c"
	flagsetKeyA                         = "k-a"
	flagsetKeyB                         = "k-b"
	kMove                               = "k-move"
	keepKey                             = "keep-key"
	keyC                                = "key-c"
	modifyingAFlagsetIs                 = "modifying a flagset is not supported at runtime"
	newAPIKey                           = "new-api-key"
	oldAPIKey                           = "old-api-key"
	oldKeyOne                           = "old-key-1"
	oldKeyTwo                           = "old-key-2"
	originalKey                         = "original-key"
	testFlagsetOne                      = "test-flagset-1"
	testFlagsetTwo                      = "test-flagset-2"
	testdataSemaphoreConfigurationFlags = "../testdata/semaphore/configuration_flags.yaml"
	theNewConfigurationIs               = "the new configuration is invalid, it will not be applied"
)

func TestNewFlagsetManager(t *testing.T) {
	t.Parallel()

	flagConfig := testdataSemaphoreConfigurationFlags
	tests := []struct {
		config      *config.Config
		logger      *slog.Logger
		name        string
		expectedErr string
		notifiers   []notifier.Notifier
		wantErr     bool
	}{
		{
			name:        "nil config should return error",
			config:      nil,
			logger:      slog.New(slog.DiscardHandler),
			notifiers:   []notifier.Notifier{},
			wantErr:     true,
			expectedErr: "configuration is nil",
		},
		{
			name: "empty flagsets should create default manager",
			config: &config.Config{
				FlagSets: []config.FlagSet{},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: fixtures.File,
					Path: flagConfig,
				},
			},
			logger:    slog.New(slog.DiscardHandler),
			notifiers: []notifier.Notifier{},
			wantErr:   false,
		},
		{
			name: "valid flagsets should create flagsets manager",
			config: &config.Config{
				FlagSets: []config.FlagSet{
					{
						Name: fixtures.FlagsetTest,
						Retriever: &retrieverconf.RetrieverConf{
							Kind: fixtures.File,
							Path: flagConfig,
						},
						APIKeys: []string{fixtures.APIKeyTest},
					},
				},
			},
			logger:    slog.New(slog.DiscardHandler),
			notifiers: []notifier.Notifier{},
			wantErr:   false,
		},
		{
			name: "invalid flagsets should error even if default flagset is valid",
			config: &config.Config{
				FlagSets: []config.FlagSet{
					{
						Name: "invalid-flagset",
						Retriever: &retrieverconf.RetrieverConf{
							Kind: "invalid",
							Path: flagConfig,
						},
						APIKeys: []string{fixtures.APIKeyTest},
					},
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: fixtures.File,
					Path: flagConfig,
				},
			},
			logger:    slog.New(slog.DiscardHandler),
			notifiers: []notifier.Notifier{},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			manager, err := service.NewFlagsetManager(tt.config, tt.logger, tt.notifiers, nil, nil)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, manager)

				if tt.expectedErr != "" {
					assert.Equal(t, tt.expectedErr, err.Error())
				}
			} else {
				require.NoError(t, err)

				assert.NotNil(t, manager)
				defer manager.Close()
			}
		})
	}
}

// fakeSSEService records the flagset name of every broadcast.
type fakeSSEService struct {
	stream.SSEService

	streams []string
	mu      sync.Mutex
}

func (f *fakeSSEService) BroadcastFlagChanges(flagsetName string, _ notifier.DiffCache) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.streams = append(f.streams, flagsetName)

	return nil
}

func (f *fakeSSEService) Streams() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.streams...)
}

// fakeWebsocketService counts the broadcasts.
type fakeWebsocketService struct {
	stream.WebsocketService

	broadcasts atomic.Int32
}

func (f *fakeWebsocketService) BroadcastFlagChanges(string, notifier.DiffCache) {
	f.broadcasts.Add(1)
}

// newStreamManager wires the stream services the way main.go does.
func newStreamManager(
	t *testing.T, cfg *config.Config,
) (*service.FlagsetManagerImpl, *fakeSSEService, *fakeWebsocketService) {
	t.Helper()

	sse, ws := &fakeSSEService{}, &fakeWebsocketService{}
	manager, err := service.NewFlagsetManager(cfg, slog.New(slog.DiscardHandler), nil, sse, ws)
	require.NoError(t, err)
	t.Cleanup(func() { manager.Close() })

	return manager, sse, ws
}

// assertBroadcasts checks that every flagset broadcast once on its own SSE stream and once on
// the websocket service.
func assertBroadcasts(t *testing.T, sse *fakeSSEService, ws *fakeWebsocketService, flagsets []string) {
	t.Helper()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, flagsets, sse.Streams())
		assert.Equal(c, int32(len(flagsets)), ws.broadcasts.Load())
	}, 5*time.Second, 10*time.Millisecond)
}

func TestFlagsetManager_StreamNotifiers(t *testing.T) {
	t.Parallel()

	flagConfig := testdataSemaphoreConfigurationFlags
	flagset := func(name string) config.FlagSet {
		return config.FlagSet{
			Name:      name,
			Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
			APIKeys:   []string{name + "-key"},
		}
	}

	tests := []struct {
		name         string
		config       *config.Config
		wantFlagsets []string
	}{
		{
			name: fixtures.DefaultMode,
			config: &config.Config{
				Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
			},
			wantFlagsets: []string{utils.DefaultFlagSetName},
		},
		{
			name: "flagsets mode",
			config: &config.Config{
				FlagSets: []config.FlagSet{flagset(fixtures.FlagsetOne), flagset(fixtures.FlagsetTwo)},
			},
			wantFlagsets: []string{fixtures.FlagsetOne, fixtures.FlagsetTwo},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, sse, ws := newStreamManager(t, tt.config)
			assertBroadcasts(t, sse, ws, tt.wantFlagsets)
		})
	}

	t.Run("flagset added at runtime", func(t *testing.T) {
		t.Parallel()

		manager, sse, wsSvc := newStreamManager(t, &config.Config{
			FlagSets: []config.FlagSet{flagset(fixtures.FlagsetOne)},
		})
		assertBroadcasts(t, sse, wsSvc, []string{fixtures.FlagsetOne})

		manager.OnConfigChange(&config.Config{
			FlagSets: []config.FlagSet{flagset(fixtures.FlagsetOne), flagset(fixtures.FlagsetTwo)},
		})
		assertBroadcasts(t, sse, wsSvc, []string{fixtures.FlagsetOne, fixtures.FlagsetTwo})
	})
}

func TestFlagsetManager_FlagSet(t *testing.T) {
	t.Parallel()

	flagConfig := testdataSemaphoreConfigurationFlags
	file := &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig}

	tests := []struct {
		name   string
		config *config.Config
		apiKey string
		// wantFlagset is the name under which AllFlagSets lists the flagset that FlagSet must return.
		wantFlagset string
		wantErr     string
	}{
		{
			name: "flagset mode, valid api key",
			config: &config.Config{FlagSets: []config.FlagSet{
				{Name: fixtures.FlagsetTest, Retriever: file, APIKeys: []string{fixtures.APIKeyTest}},
			}},
			apiKey:      fixtures.APIKeyTest,
			wantFlagset: fixtures.FlagsetTest,
		},
		{
			name: "flagset mode, invalid api key",
			config: &config.Config{FlagSets: []config.FlagSet{
				{Name: fixtures.FlagsetTest, Retriever: file, APIKeys: []string{fixtures.APIKeyTest}},
			}},
			apiKey:  "invalid-key",
			wantErr: "flagset not found for API key",
		},
		{
			name: "flagset mode, empty api key",
			config: &config.Config{FlagSets: []config.FlagSet{
				{Name: fixtures.FlagsetTest, Retriever: file, APIKeys: []string{fixtures.APIKeyTest}},
			}},
			apiKey:  "",
			wantErr: "no API key provided",
		},
		{
			name:        "default mode, empty api key should work",
			config:      &config.Config{FlagSets: []config.FlagSet{}, Retriever: file},
			apiKey:      "",
			wantFlagset: fixtures.FlagsetDefault,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			manager, err := service.NewFlagsetManager(
				tt.config,
				slog.New(slog.DiscardHandler),
				[]notifier.Notifier{},
				nil,
				nil,
			)
			require.NoError(t, err, failedToCreateFlagsetManager)

			defer manager.Close()

			got, err := manager.FlagSet(tt.apiKey)

			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				assert.Nil(t, got)

				return
			}

			require.NoError(t, err)
			all, err := manager.AllFlagSets()
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Same(t, all[tt.wantFlagset], got)
		})
	}
}

func TestFlagsetManager_FlagSetName(t *testing.T) {
	t.Parallel()

	flagConfig := testdataSemaphoreConfigurationFlags

	// Test flagset mode
	t.Run(fixtures.FlagsetMode, func(t *testing.T) {
		t.Parallel()

		conf := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetTest,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{fixtures.APIKeyTest},
				},
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(conf, logger, notifiers, nil, nil)
		require.NoError(t, err, failedToCreateFlagsetManager)

		assert.NotNil(t, manager)
		defer manager.Close()

		t.Run("existing api key", func(t *testing.T) {
			t.Parallel()

			name, flagSetNameErr := manager.FlagSetName(fixtures.APIKeyTest)
			require.NoError(t, flagSetNameErr)
			assert.Equal(t, fixtures.FlagsetTest, name)
		})
		t.Run("non-existing api key", func(t *testing.T) {
			t.Parallel()

			name, flagSetNameErr := manager.FlagSetName("invalid-key")
			require.Error(t, flagSetNameErr)
			assert.Empty(t, name)
			assert.Equal(t, "no flag set associated to the API key", flagSetNameErr.Error())
		})
		t.Run("empty api key", func(t *testing.T) {
			t.Parallel()

			name, flagSetNameErr := manager.FlagSetName("")
			require.Error(t, flagSetNameErr)
			assert.Empty(t, name)
			assert.Equal(t, "apiKey is required when using flagsets", flagSetNameErr.Error())
		})
	})

	// Test default mode
	t.Run(fixtures.DefaultMode, func(t *testing.T) {
		t.Parallel()

		conf := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: fixtures.File,
				Path: flagConfig,
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(conf, logger, notifiers, nil, nil)
		require.NoError(t, err, failedToCreateFlagsetManager)

		assert.NotNil(t, manager)
		defer manager.Close()

		t.Run("empty api key should return default", func(t *testing.T) {
			t.Parallel()

			name, flagSetNameErr := manager.FlagSetName("")
			require.NoError(t, flagSetNameErr)
			assert.Equal(t, fixtures.FlagsetDefault, name)
		})
	})
}

func TestFlagsetManager_AllFlagSets(t *testing.T) {
	t.Parallel()

	flagConfig := testdataSemaphoreConfigurationFlags

	// Test flagset mode
	t.Run(fixtures.FlagsetMode, func(t *testing.T) {
		t.Parallel()

		conf := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: testFlagsetOne,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{apiKeyOne},
				},
				{
					Name: testFlagsetTwo,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{apiKeyTwo},
				},
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(conf, logger, notifiers, nil, nil)
		require.NoError(t, err, failedToCreateFlagsetManager)

		assert.NotNil(t, manager)
		defer manager.Close()

		flagsets, err := manager.AllFlagSets()
		require.NoError(t, err)
		assert.Len(t, flagsets, 2)
		assert.Contains(t, flagsets, testFlagsetOne)
		assert.Contains(t, flagsets, testFlagsetTwo)
	})

	t.Run("flagset mode using default flagset name", func(t *testing.T) {
		t.Parallel()

		conf := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetDefault,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{apiKeyOne},
				},
				{
					Name: testFlagsetTwo,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{apiKeyTwo},
				},
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(conf, logger, notifiers, nil, nil)
		require.NoError(t, err, failedToCreateFlagsetManager)

		assert.NotNil(t, manager)
		defer manager.Close()

		flagsets, err := manager.AllFlagSets()
		require.NoError(t, err)
		assert.Len(t, flagsets, 2)
		assert.NotContains(t, flagsets, fixtures.FlagsetDefault)
		assert.Contains(t, flagsets, testFlagsetTwo)
	})

	// Test default mode
	t.Run(fixtures.DefaultMode, func(t *testing.T) {
		t.Parallel()

		conf := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: fixtures.File,
				Path: flagConfig,
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(conf, logger, notifiers, nil, nil)
		require.NoError(t, err, failedToCreateFlagsetManager)

		assert.NotNil(t, manager)
		defer manager.Close()

		flagsets, err := manager.AllFlagSets()
		require.NoError(t, err)
		assert.Len(t, flagsets, 1)
		assert.Contains(t, flagsets, fixtures.FlagsetDefault)
	})
}

func TestFlagsetManager_Default(t *testing.T) {
	t.Parallel()

	flagConfig := testdataSemaphoreConfigurationFlags

	// Test default mode
	t.Run(fixtures.DefaultMode, func(t *testing.T) {
		t.Parallel()

		conf := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: fixtures.File,
				Path: flagConfig,
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(conf, logger, notifiers, nil, nil)
		require.NoError(t, err, failedToCreateFlagsetManager)

		assert.NotNil(t, manager)
		defer manager.Close()

		all, err := manager.AllFlagSets()
		require.NoError(t, err)
		require.NotNil(t, manager.Default())
		assert.Same(t, all[fixtures.FlagsetDefault], manager.Default())
	})

	// Test flagset mode
	t.Run(fixtures.FlagsetMode, func(t *testing.T) {
		t.Parallel()

		conf := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetTest,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{fixtures.APIKeyTest},
				},
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(conf, logger, notifiers, nil, nil)
		require.NoError(t, err, failedToCreateFlagsetManager)

		assert.NotNil(t, manager)
		defer manager.Close()

		defaultFlagset := manager.Default()
		assert.Nil(t, defaultFlagset)
	})
}

func TestFlagsetManager_IsDefaultFlagSet(t *testing.T) {
	t.Parallel()

	flagConfig := testdataSemaphoreConfigurationFlags

	// Test default mode
	t.Run(fixtures.DefaultMode, func(t *testing.T) {
		t.Parallel()

		conf := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: fixtures.File,
				Path: flagConfig,
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(conf, logger, notifiers, nil, nil)
		require.NoError(t, err, failedToCreateFlagsetManager)

		assert.NotNil(t, manager)
		defer manager.Close()

		assert.True(t, manager.IsDefaultFlagSet())
	})

	// Test flagset mode
	t.Run(fixtures.FlagsetMode, func(t *testing.T) {
		t.Parallel()

		conf := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetTest,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{fixtures.APIKeyTest},
				},
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(conf, logger, notifiers, nil, nil)
		require.NoError(t, err, failedToCreateFlagsetManager)

		assert.NotNil(t, manager)
		defer manager.Close()

		assert.False(t, manager.IsDefaultFlagSet())
	})
}

func TestFlagsetManager_Close(t *testing.T) {
	t.Parallel()

	flagConfig := testdataSemaphoreConfigurationFlags

	// Test default mode
	t.Run(fixtures.DefaultMode, func(t *testing.T) {
		t.Parallel()

		outputDir := t.TempDir()
		conf := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: fixtures.File,
				Path: flagConfig,
			},
			Exporter: bufferedFileExporter(outputDir),
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(conf, logger, notifiers, nil, nil)
		require.NoError(t, err, failedToCreateFlagsetManager)
		assert.NotNil(t, manager)

		_, _ = manager.Default().
			BoolVariation("test-flag-rule-apply", ffcontext.NewEvaluationContext(fixtures.UserKeyUser), false)
		assertFlushedOnClose(t, manager, outputDir)
	})

	// Test flagset mode
	t.Run(fixtures.FlagsetMode, func(t *testing.T) {
		t.Parallel()

		outputDir := t.TempDir()
		conf := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetTest,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					Exporter: bufferedFileExporter(outputDir),
					APIKeys:  []string{fixtures.APIKeyTest},
				},
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(conf, logger, notifiers, nil, nil)
		require.NoError(t, err, failedToCreateFlagsetManager)
		assert.NotNil(t, manager)

		flagset, err := manager.FlagSet(fixtures.APIKeyTest)
		require.NoError(t, err)

		_, _ = flagset.BoolVariation(
			"test-flag-rule-apply",
			ffcontext.NewEvaluationContext(fixtures.UserKeyUser),
			false,
		)

		assertFlushedOnClose(t, manager, outputDir)
	})
}

// bufferedFileExporter returns a file exporter whose flush interval is long enough
// that events stay in memory until the flagset manager is closed.
func bufferedFileExporter(outputDir string) *config.ExporterConf {
	return &config.ExporterConf{
		Kind:             config.FileExporter,
		OutputDir:        outputDir,
		FlushInterval:    time.Hour.Milliseconds(),
		MaxEventInMemory: 10_000,
	}
}

// assertFlushedOnClose checks that the buffered events are only written once the manager is closed.
func assertFlushedOnClose(t *testing.T, manager service.FlagsetManager, outputDir string) {
	t.Helper()

	files, err := os.ReadDir(outputDir)
	require.NoError(t, err)
	require.Empty(t, files, "events should still be buffered before Close")

	manager.Close()

	files, err = os.ReadDir(outputDir)
	require.NoError(t, err)
	assert.NotEmpty(t, files, "Close should flush the buffered events to the exporter")
}

// setupManager is a helper function to create a manager and capture log messages.
func setupManager(t *testing.T, cfg *config.Config) (*service.FlagsetManagerImpl, *slogutil.ObservedLogs) {
	t.Helper()

	obs, logs := slogutil.NewObserver(slog.LevelError)
	logger := obs
	manager, err := service.NewFlagsetManager(cfg, logger, nil, nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { manager.Close() })

	return manager, logs
}
func TestFlagsetManager_OnConfigChange(t *testing.T) {
	t.Parallel()

	flagConfig := testdataSemaphoreConfigurationFlags

	t.Run("should reject switching from default to flagsets mode", func(t *testing.T) {
		t.Parallel()

		// Create manager in default mode
		cfg := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: fixtures.File,
				Path: flagConfig,
			},
		}
		manager, logs := setupManager(t, cfg)

		// Try to switch to flagsets mode
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "new-flagset",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{fixtures.UserKeyNew},
				},
			},
		}

		assert.NotPanics(t, func() {
			manager.OnConfigChange(newConfig)
		})

		// Verify that the error log was displayed
		assert.Equal(t, 1, logs.Len(), expectedExactlyOneError)
		logEntry := logs.All()[0]
		assert.Equal(t, slog.LevelError, logEntry.Level)
		assert.Contains(
			t,
			logEntry.Message,
			"switching from default to flagsets mode (or the opposite) is not supported during runtime",
		)
	})

	t.Run("should reject switching from flagsets to default mode", func(t *testing.T) {
		t.Parallel()

		// Create manager in flagsets mode
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetTest,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{fixtures.APIKeyTest},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		// Try to switch to default mode
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: fixtures.File,
				Path: flagConfig,
			},
		}

		// Should not panic and should reject the change
		assert.NotPanics(t, func() {
			manager.OnConfigChange(newConfig)
		})
		// Verify that the error log was displayed
		assert.Equal(t, 1, logs.Len(), expectedExactlyOneError)
		logEntry := logs.All()[0]
		assert.Equal(t, slog.LevelError, logEntry.Level)
		assert.Contains(
			t,
			logEntry.Message,
			"switching from default to flagsets mode (or the opposite) is not supported during runtime",
		)
	})

	t.Run("should update AuthorizedKeys in default mode", func(t *testing.T) {
		t.Parallel()

		// Create manager in default mode
		cfg := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: fixtures.File,
				Path: flagConfig,
			},
			AuthorizedKeys: config.APIKeys{
				Evaluation: []string{fixtures.UserKeyOld},
			},
		}
		manager, _ := setupManager(t, cfg)

		// Update AuthorizedKeys
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: fixtures.File,
				Path: flagConfig,
			},
			AuthorizedKeys: config.APIKeys{
				Evaluation: []string{fixtures.UserKeyNew},
			},
		}

		manager.OnConfigChange(newConfig)

		// Verify the key was updated - ForceReloadAPIKeys resets and reloads
		assert.False(t, cfg.APIKeyExists(fixtures.UserKeyOld))
		assert.True(t, cfg.APIKeyExists(fixtures.UserKeyNew))
	})

	t.Run("should reject invalid configuration in default mode - missing retriever", func(t *testing.T) {
		t.Parallel()

		// Create manager in default mode
		cfg := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: fixtures.File,
				Path: flagConfig,
			},
			AuthorizedKeys: config.APIKeys{Evaluation: []string{oldAPIKey}},
		}
		// Preload API keys to initialize the internal API key set
		cfg.ForceReloadAPIKeys()
		manager, logs := setupManager(t, cfg)

		// Try to update with invalid config (no retriever)
		newConfig := &config.Config{
			FlagSets:       []config.FlagSet{},
			Retriever:      nil,
			AuthorizedKeys: config.APIKeys{Evaluation: []string{newAPIKey}},
		}

		assert.NotPanics(t, func() {
			manager.OnConfigChange(newConfig)
		})

		// Verify that the error log was displayed
		assert.Equal(t, 1, logs.Len(), expectedExactlyOneError)
		logEntry := logs.All()[0]
		assert.Equal(t, slog.LevelError, logEntry.Level)
		assert.Contains(t, logEntry.Message, theNewConfigurationIs)

		// Verify the original config was not changed
		assert.True(t, cfg.APIKeyExists(oldAPIKey))
		assert.False(t, cfg.APIKeyExists(newAPIKey))
	})
}

func TestFlagsetManager_OnConfigChange_ValidationAndKeys(t *testing.T) {
	t.Parallel()

	flagConfig := testdataSemaphoreConfigurationFlags

	t.Run("should reject invalid configuration in flagsets mode - flagset with no API keys", func(t *testing.T) {
		t.Parallel()

		// Create manager in flagsets mode
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetTest,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{fixtures.APIKeyTest},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		// Try to update with invalid config (flagset with no API keys)
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetTest,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{}, // Invalid: no API keys
				},
			},
		}

		assert.NotPanics(t, func() {
			manager.OnConfigChange(newConfig)
		})

		// Verify that the error log was displayed
		assert.Equal(t, 1, logs.Len(), expectedExactlyOneError)
		logEntry := logs.All()[0]
		assert.Equal(t, slog.LevelError, logEntry.Level)
		assert.Contains(t, logEntry.Message, theNewConfigurationIs)

		// Verify the original config was not changed
		flagset, err := manager.FlagSet(fixtures.APIKeyTest)
		require.NoError(t, err)
		assert.NotNil(t, flagset)
	})

	t.Run("should reject invalid configuration in flagsets mode - duplicate API keys", func(t *testing.T) {
		t.Parallel()

		// Create manager in flagsets mode
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: testFlagsetOne,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{apiKeyOne},
				},
				{
					Name: testFlagsetTwo,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{apiKeyTwo},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		// Try to update with invalid config (duplicate API keys across flagsets)
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: testFlagsetOne,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{"duplicate-key"},
				},
				{
					Name: testFlagsetTwo,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{"duplicate-key"}, // Invalid: duplicate API key
				},
			},
		}

		assert.NotPanics(t, func() {
			manager.OnConfigChange(newConfig)
		})

		// Verify that the error log was displayed
		assert.Equal(t, 1, logs.Len(), expectedExactlyOneError)
		logEntry := logs.All()[0]
		assert.Equal(t, slog.LevelError, logEntry.Level)
		assert.Contains(t, logEntry.Message, theNewConfigurationIs)

		// Verify the original config was not changed
		flagset1, err := manager.FlagSet(apiKeyOne)
		require.NoError(t, err)
		assert.NotNil(t, flagset1)

		flagset2, err := manager.FlagSet(apiKeyTwo)
		require.NoError(t, err)
		assert.NotNil(t, flagset2)
	})
}

func TestFlagsetManager_OnConfigChange_APIKeys(t *testing.T) {
	t.Parallel()

	flagConfig := testdataSemaphoreConfigurationFlags

	t.Run("should successfully update API keys in flagsets mode", func(t *testing.T) {
		t.Parallel()

		// Create manager in flagsets mode
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetTest,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{oldAPIKey},
				},
			},
		}
		manager, _ := setupManager(t, cfg)

		// Verify old key works before change
		flagset, err := manager.FlagSet(oldAPIKey)
		require.NoError(t, err)
		assert.NotNil(t, flagset)

		// Update API key
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetTest,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{newAPIKey},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// Verify the key was updated: the old one is no longer routed, the new one reaches the same flagset
		assertKeyRouting(t, manager, map[string]string{oldAPIKey: "", newAPIKey: fixtures.FlagsetTest})
		_, err = manager.FlagSet(oldAPIKey)
		require.Error(t, err, "old API key should no longer work")
		flagset, err = manager.FlagSet(newAPIKey)
		require.NoError(t, err, "new API key should work")
		assert.NotNil(t, flagset)
	})

	t.Run("should add new API keys to existing flagset", func(t *testing.T) {
		t.Parallel()

		// Create manager in flagsets mode with single key
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetTest,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{apiKeyOne},
				},
			},
		}
		manager, _ := setupManager(t, cfg)

		// Verify initial state
		_, err := manager.FlagSet(apiKeyOne)
		require.NoError(t, err)
		_, err = manager.FlagSet(apiKeyTwo)
		require.Error(t, err, "api-key-2 should not work yet")

		// Add second API key
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetTest,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{apiKeyOne, apiKeyTwo},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// Verify both keys work now
		flagset1, err := manager.FlagSet(apiKeyOne)
		require.NoError(t, err)
		assert.NotNil(t, flagset1)

		flagset2, err := manager.FlagSet(apiKeyTwo)
		require.NoError(t, err)
		assert.NotNil(t, flagset2)

		// Both should point to the same flagset
		name1, _ := manager.FlagSetName(apiKeyOne)
		name2, _ := manager.FlagSetName(apiKeyTwo)
		assert.Equal(t, name1, name2)
	})

	t.Run("should remove API keys from flagset", func(t *testing.T) {
		t.Parallel()

		// Create manager with two API keys
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetTest,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{apiKeyOne, apiKeyTwo},
				},
			},
		}
		manager, _ := setupManager(t, cfg)

		// Verify both keys work initially
		_, err := manager.FlagSet(apiKeyOne)
		require.NoError(t, err)
		_, err = manager.FlagSet(apiKeyTwo)
		require.NoError(t, err)

		// Remove one API key
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetTest,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{apiKeyOne},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// api-key-1 still reaches the flagset, api-key-2 is no longer routed
		assertKeyRouting(t, manager, map[string]string{apiKeyOne: fixtures.FlagsetTest, apiKeyTwo: ""})
		flagset, err := manager.FlagSet(apiKeyOne)
		require.NoError(t, err)
		assert.NotNil(t, flagset)

		_, err = manager.FlagSet(apiKeyTwo)
		assert.Error(t, err, "api-key-2 should no longer work")
	})

	t.Run("should move API key between flagsets", func(t *testing.T) {
		t.Parallel()

		// Create manager with two flagsets
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetOne,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{apiKeyOne, apiKeyMove},
				},
				{
					Name: fixtures.FlagsetTwo,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{apiKeyTwo},
				},
			},
		}
		manager, _ := setupManager(t, cfg)

		// Verify initial state
		name, err := manager.FlagSetName(apiKeyMove)
		require.NoError(t, err)
		assert.Equal(t, fixtures.FlagsetOne, name)

		// Move api-key-move from flagset-1 to flagset-2
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetOne,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{apiKeyOne},
				},
				{
					Name: fixtures.FlagsetTwo,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{apiKeyTwo, apiKeyMove},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// Verify api-key-move now points to flagset-2
		name, err = manager.FlagSetName(apiKeyMove)
		require.NoError(t, err)
		assert.Equal(t, fixtures.FlagsetTwo, name)

		// Verify other keys still work correctly
		name1, _ := manager.FlagSetName(apiKeyOne)
		assert.Equal(t, fixtures.FlagsetOne, name1)

		name2, _ := manager.FlagSetName(apiKeyTwo)
		assert.Equal(t, fixtures.FlagsetTwo, name2)
	})

	t.Run("should rotate API keys across flagsets regardless of processing order", func(t *testing.T) {
		t.Parallel()

		// Cyclic rotation in a single reload: ka: A->B, kb: B->C, kc: C->A.
		// This is order-sensitive with incremental per-flagset updates, so it guards against a
		// regression of the routing rebuild.
		newFlagset := func(name, key string) config.FlagSet {
			return config.FlagSet{
				Name:      name,
				Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
				APIKeys:   []string{key},
			}
		}
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				newFlagset(fixtures.FlagsetLower, "ka"),
				newFlagset(flagsetB, "kb"),
				newFlagset(flagsetC, "kc"),
			},
		}
		manager, _ := setupManager(t, cfg)

		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				newFlagset(fixtures.FlagsetLower, "kc"),
				newFlagset(flagsetB, "ka"),
				newFlagset(flagsetC, "kb"),
			},
		}
		manager.OnConfigChange(newConfig)

		name, err := manager.FlagSetName("ka")
		require.NoError(t, err)
		assert.Equal(t, flagsetB, name)
		name, err = manager.FlagSetName("kb")
		require.NoError(t, err)
		assert.Equal(t, flagsetC, name)
		name, err = manager.FlagSetName("kc")
		require.NoError(t, err)
		assert.Equal(t, fixtures.FlagsetLower, name)
	})
}

func TestFlagsetManager_OnConfigChange_SpecialFlagsets(t *testing.T) {
	t.Parallel()

	flagConfig := testdataSemaphoreConfigurationFlags

	t.Run("should not update when config is unchanged in flagsets mode", func(t *testing.T) {
		t.Parallel()

		// Create manager in flagsets mode
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetTest,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{fixtures.APIKeyTest},
				},
			},
		}
		obs, logs := slogutil.NewObserver(slog.LevelInfo)
		logger := obs
		manager, err := service.NewFlagsetManager(cfg, logger, nil, nil, nil)
		require.NoError(t, err)
		t.Cleanup(func() { manager.Close() })

		// Apply the same config again
		sameConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetTest,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{fixtures.APIKeyTest},
				},
			},
		}

		manager.OnConfigChange(sameConfig)

		// Verify no "Configuration changed" log was produced
		configChangeLogs := logs.FilterMessage("Configuration changed: updating the APIKeys for flagset")
		assert.Equal(t, 0, configChangeLogs.Len(), "No config change log should be produced when config is unchanged")

		// Verify the key still works
		flagset, err := manager.FlagSet(fixtures.APIKeyTest)
		require.NoError(t, err)
		assert.NotNil(t, flagset)
	})

	t.Run("should not update API keys for flagset with empty name", func(t *testing.T) {
		t.Parallel()

		// Create manager with a flagset that has an empty name (gets auto-generated)
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "", // Empty name - will be auto-generated
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{originalKey},
				},
			},
		}
		obs, logs := slogutil.NewObserver(slog.LevelWarn)
		logger := obs
		manager, err := service.NewFlagsetManager(cfg, logger, nil, nil, nil)
		require.NoError(t, err)
		t.Cleanup(func() { manager.Close() })

		// Verify the warning about empty name was logged
		warnLogs := logs.FilterMessageSnippet("no flagset name provided")
		assert.Equal(t, 1, warnLogs.Len(), "Expected warning about empty flagset name")

		// Verify original key works
		flagset, err := manager.FlagSet(originalKey)
		require.NoError(t, err)
		assert.NotNil(t, flagset)

		// Try to update config with empty name flagset - should be ignored
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "", // Still empty
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{fixtures.UserKeyNew}, // Try to change key
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// Original key should still work because empty name flagsets can't be updated
		flagset, err = manager.FlagSet(originalKey)
		require.NoError(t, err)
		assert.NotNil(t, flagset)
	})

	t.Run("should not update API keys for flagset named default", func(t *testing.T) {
		t.Parallel()

		// Create manager with a flagset named "default" (gets auto-generated)
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetDefault, // Reserved name - will be auto-generated
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{originalKey},
				},
			},
		}
		obs, logs := slogutil.NewObserver(slog.LevelWarn)
		logger := obs
		manager, err := service.NewFlagsetManager(cfg, logger, nil, nil, nil)
		require.NoError(t, err)
		t.Cleanup(func() { manager.Close() })

		// Verify the warning about using 'default' name was logged
		warnLogs := logs.FilterMessageSnippet("using 'default' as a flagset name")
		assert.Equal(t, 1, warnLogs.Len(), "Expected warning about using 'default' as flagset name")

		// Verify original key works
		flagset, err := manager.FlagSet(originalKey)
		require.NoError(t, err)
		assert.NotNil(t, flagset)

		// Try to update config with "default" name flagset - should be ignored
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetDefault,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{fixtures.UserKeyNew},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// Original key should still work because "default" name flagsets can't be updated
		flagset, err = manager.FlagSet(originalKey)
		require.NoError(t, err)
		assert.NotNil(t, flagset)
	})

	t.Run("should update multiple flagsets API keys simultaneously", func(t *testing.T) {
		t.Parallel()

		// Create manager with multiple flagsets
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetOne,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{oldKeyOne},
				},
				{
					Name: fixtures.FlagsetTwo,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{oldKeyTwo},
				},
			},
		}
		manager, _ := setupManager(t, cfg)

		// Verify initial state
		name1, _ := manager.FlagSetName(oldKeyOne)
		assert.Equal(t, fixtures.FlagsetOne, name1)

		name2, _ := manager.FlagSetName(oldKeyTwo)
		assert.Equal(t, fixtures.FlagsetTwo, name2)

		// Update both flagsets
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: fixtures.FlagsetOne,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{"new-key-1"},
				},
				{
					Name: fixtures.FlagsetTwo,
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: flagConfig,
					},
					APIKeys: []string{"new-key-2"},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// Verify old keys no longer work
		_, err := manager.FlagSet(oldKeyOne)
		require.Error(t, err)
		_, err = manager.FlagSet(oldKeyTwo)
		require.Error(t, err)

		// Verify new keys work and point to correct flagsets
		name1, err = manager.FlagSetName("new-key-1")
		require.NoError(t, err)
		assert.Equal(t, fixtures.FlagsetOne, name1)

		name2, err = manager.FlagSetName("new-key-2")
		require.NoError(t, err)
		assert.Equal(t, fixtures.FlagsetTwo, name2)
	})

	t.Run("should only notify the websocket clients of the flagset added at runtime", func(t *testing.T) {
		t.Parallel()

		flagset := func(name, apiKey string) config.FlagSet {
			return config.FlagSet{
				Name:      name,
				Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
				APIKeys:   []string{apiKey},
			}
		}
		existing := flagset(fixtures.FlagsetOne, fixtures.UserKeyFirst)
		// flagset-1 never notifies, so its client must not receive anything.
		existing.DisableNotifierOnInit = true

		wsService := stream.NewWebsocketService()
		manager, err := service.NewFlagsetManager(
			&config.Config{FlagSets: []config.FlagSet{existing}}, slog.New(slog.DiscardHandler), nil, nil, wsService)
		require.NoError(t, err)
		t.Cleanup(func() { manager.Close() })

		conn1, conn2 := &countingWSConn{}, &countingWSConn{}
		wsService.Register(conn1, fixtures.FlagsetOne)
		wsService.Register(conn2, fixtures.FlagsetTwo)

		manager.OnConfigChange(&config.Config{
			FlagSets: []config.FlagSet{existing, flagset(fixtures.FlagsetTwo, fixtures.UserKeySecond)},
		})

		// The flags loaded by the new flagset are notified as added.
		assert.Eventually(t, func() bool { return conn2.writes.Load() > 0 },
			5*time.Second, 10*time.Millisecond)
		// Close waits for the in-flight broadcast, so every client it targets has been written.
		wsService.Close()
		assert.Zero(t, conn1.writes.Load())
	})
}

func TestFlagsetManager_OnConfigChange_FlagsetLifecycle(t *testing.T) {
	t.Parallel()

	flagConfig := testdataSemaphoreConfigurationFlags

	t.Run("should add a new flagset at runtime", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      fixtures.FlagsetOne,
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{fixtures.UserKeyFirst},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		// The second flagset does not exist yet.
		_, err := manager.FlagSet(fixtures.UserKeySecond)
		require.Error(t, err)

		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      fixtures.FlagsetOne,
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{fixtures.UserKeyFirst},
				},
				{
					Name:      fixtures.FlagsetTwo,
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{fixtures.UserKeySecond},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// The new flagset is now available and the existing one still works.
		flagset2, err := manager.FlagSet(fixtures.UserKeySecond)
		require.NoError(t, err, "the flagset added at runtime should be available")
		assert.NotNil(t, flagset2)

		name2, err := manager.FlagSetName(fixtures.UserKeySecond)
		require.NoError(t, err)
		assert.Equal(t, fixtures.FlagsetTwo, name2)

		flagset1, err := manager.FlagSet(fixtures.UserKeyFirst)
		require.NoError(t, err)
		assert.NotNil(t, flagset1)

		all, err := manager.AllFlagSets()
		require.NoError(t, err)
		assert.Len(t, all, 2)

		// Adding a flagset must not produce any error log.
		assert.Equal(t, 0, logs.Len(), "adding a flagset should not log any error")
	})

	t.Run("should remove a flagset at runtime", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      fixtures.FlagsetOne,
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{fixtures.UserKeyFirst},
				},
				{
					Name:      fixtures.FlagsetTwo,
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{fixtures.UserKeySecond},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		// Both flagsets exist initially.
		_, err := manager.FlagSet(fixtures.UserKeyFirst)
		require.NoError(t, err)
		_, err = manager.FlagSet(fixtures.UserKeySecond)
		require.NoError(t, err)

		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      fixtures.FlagsetOne,
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{fixtures.UserKeyFirst},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// flagset-2 is gone, flagset-1 still works.
		flagset1, err := manager.FlagSet(fixtures.UserKeyFirst)
		require.NoError(t, err)
		assert.NotNil(t, flagset1)

		_, err = manager.FlagSet(fixtures.UserKeySecond)
		require.Error(t, err, "the removed flagset should no longer be available")

		all, err := manager.AllFlagSets()
		require.NoError(t, err)
		assert.Len(t, all, 1)

		assert.Equal(t, 0, logs.Len(), "removing a flagset should not log any error")
	})

	t.Run("should reject modifying an existing flagset configuration", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:            fixtures.FlagsetOne,
					Retriever:       &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					PollingInterval: 0,
					APIKeys:         []string{fixtures.UserKeyFirst},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		// Change the polling interval (a non-API-key field) -> forbidden.
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:            fixtures.FlagsetOne,
					Retriever:       &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					PollingInterval: 5000,
					APIKeys:         []string{fixtures.UserKeyFirst},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// The flagset is still served (unchanged) and an error is logged.
		flagset, err := manager.FlagSet(fixtures.UserKeyFirst)
		require.NoError(t, err)
		assert.NotNil(t, flagset)

		rejectedLogs := logs.FilterMessageSnippet(modifyingAFlagsetIs)
		assert.Equal(t, 1, rejectedLogs.Len(), "expected an error log about the rejected modification")
		assert.Contains(t, rejectedLogs.All()[0].Message, "next time the relay proxy is restarted")
		assert.Equal(t, fixtures.FlagsetOne, rejectedLogs.All()[0].ContextMap()["flagset"])
		// No bundled API-key error since the API keys did not change.
		assert.Equal(t, 0, logs.FilterMessageSnippet("API keys change for this flagset is also ignored").Len())
	})
}

func TestFlagsetManager_OnConfigChange_FlagsetModifications(t *testing.T) {
	t.Parallel()

	flagConfig := testdataSemaphoreConfigurationFlags

	t.Run("should reject a bundled API key and configuration change", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:            fixtures.FlagsetOne,
					Retriever:       &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					PollingInterval: 0,
					APIKeys:         []string{fixtures.UserKeyOld},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		// Change both the polling interval and the API key -> the whole change is rejected.
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:            fixtures.FlagsetOne,
					Retriever:       &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					PollingInterval: 5000,
					APIKeys:         []string{fixtures.UserKeyNew},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// The old key still works and the new key is NOT applied.
		flagset, err := manager.FlagSet(fixtures.UserKeyOld)
		require.NoError(t, err, "the old API key must still work because the change is rejected")
		assert.NotNil(t, flagset)

		_, err = manager.FlagSet(fixtures.UserKeyNew)
		require.Error(t, err, "the bundled API key change must not be applied")

		// Two error logs: the modification rejection and the explicit bundled-key rejection.
		assert.Equal(t, 1, logs.FilterMessageSnippet(modifyingAFlagsetIs).Len())
		assert.Equal(t, 1, logs.FilterMessageSnippet("API keys change for this flagset is also ignored").Len())
	})

	t.Run("should apply add, remove and key change while rejecting a modification in one reload", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      fixtures.FlagsetLower,
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{fixtures.UserKeyA},
				},
				{
					Name:      flagsetB,
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{"key-b"},
				},
				{
					Name:            flagsetC,
					Retriever:       &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					PollingInterval: 0,
					APIKeys:         []string{keyC},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				// flagset-a: unchanged
				{
					Name:      fixtures.FlagsetLower,
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{fixtures.UserKeyA},
				},
				// flagset-b: removed (absent)
				// flagset-c: forbidden modification (polling interval changed)
				{
					Name:            flagsetC,
					Retriever:       &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					PollingInterval: 5000,
					APIKeys:         []string{keyC},
				},
				// flagset-d: added
				{
					Name:      "flagset-d",
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{"key-d"},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// a unchanged, b removed, c kept (modification rejected), d added.
		_, err := manager.FlagSet(fixtures.UserKeyA)
		require.NoError(t, err, "flagset-a should be unchanged")
		_, err = manager.FlagSet("key-b")
		require.Error(t, err, "flagset-b should have been removed")
		_, err = manager.FlagSet(keyC)
		require.NoError(t, err, "flagset-c should be kept because its modification was rejected")
		_, err = manager.FlagSet("key-d")
		require.NoError(t, err, "flagset-d should have been added")

		all, err := manager.AllFlagSets()
		require.NoError(t, err)
		assert.Len(t, all, 3, "expected flagset-a, flagset-c and flagset-d")

		// Only flagset-c's modification is rejected.
		assert.Equal(t, 1, logs.FilterMessageSnippet(modifyingAFlagsetIs).Len())
	})

	t.Run("should leave unnamed flagsets untouched and warn when their count changes", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      "flagset-named",
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{"named-key"},
				},
				{
					Name:      "", // unnamed -> gets a generated name at startup
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{"unnamed-key"},
				},
			},
		}
		obs, logs := slogutil.NewObserver(slog.LevelWarn)
		logger := obs
		manager, err := service.NewFlagsetManager(cfg, logger, nil, nil, nil)
		require.NoError(t, err)
		t.Cleanup(func() { manager.Close() })

		// New config drops the unnamed flagset (which cannot be live-removed).
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      "flagset-named",
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{"named-key"},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// The unnamed flagset is left running (not removed) and a warning is logged.
		flagset, err := manager.FlagSet("unnamed-key")
		require.NoError(t, err, "the unnamed flagset must keep working until a restart")
		assert.NotNil(t, flagset)

		warnLogs := logs.FilterMessageSnippet("without an explicit name is not supported at runtime")
		assert.Equal(t, 1, warnLogs.Len(), "expected a warning about unnamed flagsets")
	})
}

func TestFlagsetManager_OnConfigChange_FlagsetEdgeCases(t *testing.T) {
	t.Parallel()

	flagConfig := testdataSemaphoreConfigurationFlags

	t.Run("should fail closed when an accepted key move collides with a rejected modification", func(t *testing.T) {
		t.Parallel()

		// fs-a gains "k-move" (accepted) while fs-b — which still holds "k-move" — also gets a
		// forbidden retriever change (rejected, so it keeps its old keys). The reconciled config
		// then has "k-move" on BOTH flagsets even though the incoming config was valid. The key
		// must not route non-deterministically: it is left unrouted (fail closed).
		otherFlagConfig := fixtures.FlagConfigYAMLParentPath
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      "fs-a",
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{flagsetKeyA},
				},
				{
					Name:      "fs-b",
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{flagsetKeyB, kMove},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      "fs-a",
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{flagsetKeyA, kMove}, // gains k-move (accepted)
				},
				{
					Name: "fs-b",
					// Forbidden retriever change -> the whole fs-b change is rejected, so it
					// keeps its old keys [k-b, k-move].
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: otherFlagConfig},
					APIKeys:   []string{flagsetKeyB},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// Non-colliding keys still route deterministically.
		_, err := manager.FlagSet(flagsetKeyA)
		require.NoError(t, err)
		_, err = manager.FlagSet(flagsetKeyB)
		require.NoError(t, err)
		// The colliding key is failed closed: it routes to no flagset.
		_, err = manager.FlagSet(kMove)
		require.Error(t, err, "a key present on two flagsets after reconciliation must not route")

		assert.GreaterOrEqual(t,
			logs.FilterMessageSnippet("configured on multiple flagsets after reconciliation").Len(), 1,
			"expected an error log about the colliding API key")
		assert.GreaterOrEqual(t,
			logs.FilterMessageSnippet(modifyingAFlagsetIs).Len(), 1,
			"expected the forbidden modification of fs-b to be rejected")
	})

	t.Run("should skip a flagset added at runtime whose client cannot be created", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      "fs-keep",
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{keepKey},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		// The added flagset references a file that does not exist: the config is valid (the kind
		// is "file") but NewGoFeatureFlagClient fails when it tries to load the flags.
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      "fs-keep",
					Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
					APIKeys:   []string{keepKey},
				},
				{
					Name: "fs-broken",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: fixtures.File,
						Path: "/does/not/exist/flags.yaml",
					},
					APIKeys: []string{brokenKey},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// The broken flagset is not registered; the existing flagset keeps working.
		_, err := manager.FlagSet(brokenKey)
		require.Error(t, err, "a flagset whose client failed to build must not be reachable")
		_, err = manager.FlagSet(keepKey)
		require.NoError(t, err, "the pre-existing flagset must keep working")

		all, err := manager.AllFlagSets()
		require.NoError(t, err)
		assert.Len(t, all, 1)

		assert.GreaterOrEqual(t,
			logs.FilterMessageSnippet("failed to create the flagset added at runtime").Len(), 1,
			"expected an error log about the failed flagset creation")
	})
}

// TestFlagsetManager_ConcurrentReadDuringReconcile exercises the flagsetsMutex: readers hit
// FlagSet/FlagSetName/AllFlagSets while a writer repeatedly adds and removes a flagset via
// OnConfigChange. It is meant to run under -race; the stable flagset must always resolve and
// nothing may panic.
func TestFlagsetManager_ConcurrentReadDuringReconcile(t *testing.T) {
	t.Parallel()

	flagConfig := fixtures.FlagConfigYAMLParentPath
	stable := config.FlagSet{
		Name:      "fs-stable",
		Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
		APIKeys:   []string{"stable-key"},
	}
	churn := config.FlagSet{
		Name:      "fs-churn",
		Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
		APIKeys:   []string{"churn-key"},
	}
	manager, _ := setupManager(t, &config.Config{FlagSets: []config.FlagSet{stable, churn}})

	withChurn := &config.Config{FlagSets: []config.FlagSet{stable, churn}}
	withoutChurn := &config.Config{FlagSets: []config.FlagSet{stable}}

	done := make(chan struct{})

	var (
		stableErrors atomic.Int64
		wg           sync.WaitGroup
	)

	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
					_, err := manager.FlagSet("stable-key")
					if err != nil {
						stableErrors.Add(1)
					}

					_, _ = manager.AllFlagSets()
					_, _ = manager.FlagSetName("churn-key")
				}
			}
		})
	}

	wg.Go(func() {
		for i := range 100 {
			if i%2 == 0 {
				manager.OnConfigChange(withoutChurn)
			} else {
				manager.OnConfigChange(withChurn)
			}
		}

		close(done)
	})

	wg.Wait()

	// fs-stable is present in every configuration, so its key must never fail to resolve.
	assert.Equal(t, int64(0), stableErrors.Load(),
		"the stable flagset must resolve throughout concurrent add/remove of another flagset")
}

// TestFlagsetManager_RemoveFlagsetGracefullyCloses verifies that removing a flagset at runtime
// gracefully closes its GoFeatureFlag client: the data exporter is stopped and its buffered
// events are flushed (no data loss) instead of being dropped.
func TestFlagsetManager_RemoveFlagsetGracefullyCloses(t *testing.T) {
	t.Parallel()

	flagConfig := fixtures.FlagConfigYAMLParentPath

	// Webhook server that records how many times the exporter flushes to it.
	var exportCalls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		exportCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := &config.Config{
		FlagSets: []config.FlagSet{
			{
				Name:      "flagset-to-remove",
				Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
				// Bulk webhook exporter with a huge flush interval/buffer so it only flushes
				// when the flagset is closed (and not earlier during the test).
				Exporter: &config.ExporterConf{
					Kind:             config.WebhookExporter,
					EndpointURL:      server.URL,
					FlushInterval:    600000,
					MaxEventInMemory: 1000000,
				},
				APIKeys: []string{"key-remove"},
			},
			{
				// Keeper flagset so that removing the other one is not a mode switch.
				Name:      "flagset-keep",
				Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
				APIKeys:   []string{"key-keep"},
			},
		},
	}
	manager, _ := setupManager(t, cfg)

	// Capture the client of the flagset we are going to remove.
	all, err := manager.AllFlagSets()
	require.NoError(t, err)

	client := all["flagset-to-remove"]
	require.NotNil(t, client)

	// Generate one evaluation event; it is buffered (bulk exporter, not flushed yet).
	_, err = client.RawVariation("test-flag", ffcontext.NewEvaluationContext(fixtures.UserKeyUser), false)
	require.NoError(t, err)
	require.Equal(t, int64(0), exportCalls.Load(),
		"the event must still be buffered before the flagset is removed")

	// Remove the flagset at runtime (keeping flagset-keep so the mode does not change).
	newConfig := &config.Config{
		FlagSets: []config.FlagSet{
			{
				Name:      "flagset-keep",
				Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: flagConfig},
				APIKeys:   []string{"key-keep"},
			},
		},
	}
	manager.OnConfigChange(newConfig)

	// The removed flagset is no longer reachable...
	_, err = manager.FlagSet("key-remove")
	require.Error(t, err)

	// ...and its exporter was flushed during the graceful close, so the buffered event was
	// exported (no data loss). Close() flushes synchronously, so the call has completed here.
	assert.GreaterOrEqual(t, exportCalls.Load(), int64(1),
		"removing a flagset must gracefully close it and flush its buffered events")
}

// countingWSConn is a stream.WebsocketConnector counting the messages written to it.
type countingWSConn struct{ writes atomic.Int32 }

func (c *countingWSConn) WriteJSON(_ any) error {
	c.writes.Add(1)

	return nil
}

// assertKeyRouting checks, for every API key of want, the name of the flagset the manager routes it to. An empty
// name means that the key must not be routed to any flagset.
func assertKeyRouting(t *testing.T, manager service.FlagsetManager, want map[string]string) {
	t.Helper()

	got := make(map[string]string, len(want))
	for key, wantName := range want {
		name, err := manager.FlagSetName(key)
		if wantName == "" {
			require.EqualError(t, err, "no flag set associated to the API key", "key %q", key)
		} else {
			require.NoError(t, err, "key %q", key)
		}

		got[key] = name
	}

	assert.Equal(t, want, got)
}
