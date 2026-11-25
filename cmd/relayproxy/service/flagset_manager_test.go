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
	"github.com/blackbirdworks/semaphore/testutils/slogutil"
	"github.com/blackbirdworks/semaphore/utils"
)

func TestNewFlagsetManager(t *testing.T) {
	t.Parallel()

	flagConfig := "../testdata/semaphore/configuration_flags.yaml"
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
					Kind: "file",
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
						Name: "test-flagset",
						Retriever: &retrieverconf.RetrieverConf{
							Kind: "file",
							Path: flagConfig,
						},
						APIKeys: []string{"test-api-key"},
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
						APIKeys: []string{"test-api-key"},
					},
				},
				Retriever: &retrieverconf.RetrieverConf{
					Kind: "file",
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
) (service.FlagsetManager, *fakeSSEService, *fakeWebsocketService) {
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

	flagConfig := "../testdata/semaphore/configuration_flags.yaml"
	flagset := func(name string) config.FlagSet {
		return config.FlagSet{
			Name:      name,
			Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
			APIKeys:   []string{name + "-key"},
		}
	}

	tests := []struct {
		name         string
		config       *config.Config
		wantFlagsets []string
	}{
		{
			name: "default mode",
			config: &config.Config{
				Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
			},
			wantFlagsets: []string{utils.DefaultFlagSetName},
		},
		{
			name: "flagsets mode",
			config: &config.Config{
				FlagSets: []config.FlagSet{flagset("flagset-1"), flagset("flagset-2")},
			},
			wantFlagsets: []string{"flagset-1", "flagset-2"},
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

		manager, sse, ws := newStreamManager(t, &config.Config{
			FlagSets: []config.FlagSet{flagset("flagset-1")},
		})
		assertBroadcasts(t, sse, ws, []string{"flagset-1"})

		manager.OnConfigChange(&config.Config{
			FlagSets: []config.FlagSet{flagset("flagset-1"), flagset("flagset-2")},
		})
		assertBroadcasts(t, sse, ws, []string{"flagset-1", "flagset-2"})
	})
}

func TestFlagsetManager_FlagSet(t *testing.T) {
	t.Parallel()

	flagConfig := "../testdata/semaphore/configuration_flags.yaml"
	file := &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig}

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
				{Name: "test-flagset", Retriever: file, APIKeys: []string{"test-api-key"}},
			}},
			apiKey:      "test-api-key",
			wantFlagset: "test-flagset",
		},
		{
			name: "flagset mode, invalid api key",
			config: &config.Config{FlagSets: []config.FlagSet{
				{Name: "test-flagset", Retriever: file, APIKeys: []string{"test-api-key"}},
			}},
			apiKey:  "invalid-key",
			wantErr: "flagset not found for API key",
		},
		{
			name: "flagset mode, empty api key",
			config: &config.Config{FlagSets: []config.FlagSet{
				{Name: "test-flagset", Retriever: file, APIKeys: []string{"test-api-key"}},
			}},
			apiKey:  "",
			wantErr: "no API key provided",
		},
		{
			name:        "default mode, empty api key should work",
			config:      &config.Config{FlagSets: []config.FlagSet{}, Retriever: file},
			apiKey:      "",
			wantFlagset: "default",
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
			require.NoError(t, err, "failed to create FlagsetManager")
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

	flagConfig := "../testdata/semaphore/configuration_flags.yaml"

	// Test flagset mode
	t.Run("flagset mode", func(t *testing.T) {
		t.Parallel()

		config := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "test-flagset",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"test-api-key"},
				},
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(config, logger, notifiers, nil, nil)
		require.NoError(t, err, "failed to create FlagsetManager")
		assert.NotNil(t, manager)
		defer manager.Close()

		t.Run("existing api key", func(t *testing.T) {
			t.Parallel()

			name, flagSetNameErr := manager.FlagSetName("test-api-key")
			require.NoError(t, flagSetNameErr)
			assert.Equal(t, "test-flagset", name)
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
	t.Run("default mode", func(t *testing.T) {
		t.Parallel()

		config := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: "file",
				Path: flagConfig,
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(config, logger, notifiers, nil, nil)
		require.NoError(t, err, "failed to create FlagsetManager")
		assert.NotNil(t, manager)
		defer manager.Close()

		t.Run("empty api key should return default", func(t *testing.T) {
			t.Parallel()

			name, flagSetNameErr := manager.FlagSetName("")
			require.NoError(t, flagSetNameErr)
			assert.Equal(t, "default", name)
		})
	})
}

func TestFlagsetManager_AllFlagSets(t *testing.T) {
	t.Parallel()

	flagConfig := "../testdata/semaphore/configuration_flags.yaml"

	// Test flagset mode
	t.Run("flagset mode", func(t *testing.T) {
		t.Parallel()

		config := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "test-flagset-1",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"api-key-1"},
				},
				{
					Name: "test-flagset-2",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"api-key-2"},
				},
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(config, logger, notifiers, nil, nil)
		require.NoError(t, err, "failed to create FlagsetManager")
		assert.NotNil(t, manager)
		defer manager.Close()

		flagsets, err := manager.AllFlagSets()
		require.NoError(t, err)
		assert.Len(t, flagsets, 2)
		assert.Contains(t, flagsets, "test-flagset-1")
		assert.Contains(t, flagsets, "test-flagset-2")
	})

	t.Run("flagset mode using default flagset name", func(t *testing.T) {
		t.Parallel()

		config := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "default",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"api-key-1"},
				},
				{
					Name: "test-flagset-2",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"api-key-2"},
				},
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(config, logger, notifiers, nil, nil)
		require.NoError(t, err, "failed to create FlagsetManager")
		assert.NotNil(t, manager)
		defer manager.Close()

		flagsets, err := manager.AllFlagSets()
		require.NoError(t, err)
		assert.Len(t, flagsets, 2)
		assert.NotContains(t, flagsets, "default")
		assert.Contains(t, flagsets, "test-flagset-2")
	})

	// Test default mode
	t.Run("default mode", func(t *testing.T) {
		t.Parallel()

		config := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: "file",
				Path: flagConfig,
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(config, logger, notifiers, nil, nil)
		require.NoError(t, err, "failed to create FlagsetManager")
		assert.NotNil(t, manager)
		defer manager.Close()

		flagsets, err := manager.AllFlagSets()
		require.NoError(t, err)
		assert.Len(t, flagsets, 1)
		assert.Contains(t, flagsets, "default")
	})
}

func TestFlagsetManager_Default(t *testing.T) {
	t.Parallel()

	flagConfig := "../testdata/semaphore/configuration_flags.yaml"

	// Test default mode
	t.Run("default mode", func(t *testing.T) {
		t.Parallel()

		config := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: "file",
				Path: flagConfig,
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(config, logger, notifiers, nil, nil)
		require.NoError(t, err, "failed to create FlagsetManager")
		assert.NotNil(t, manager)
		defer manager.Close()

		all, err := manager.AllFlagSets()
		require.NoError(t, err)
		require.NotNil(t, manager.Default())
		assert.Same(t, all["default"], manager.Default())
	})

	// Test flagset mode
	t.Run("flagset mode", func(t *testing.T) {
		t.Parallel()

		config := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "test-flagset",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"test-api-key"},
				},
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(config, logger, notifiers, nil, nil)
		require.NoError(t, err, "failed to create FlagsetManager")
		assert.NotNil(t, manager)
		defer manager.Close()

		defaultFlagset := manager.Default()
		assert.Nil(t, defaultFlagset)
	})
}

func TestFlagsetManager_IsDefaultFlagSet(t *testing.T) {
	t.Parallel()

	flagConfig := "../testdata/semaphore/configuration_flags.yaml"

	// Test default mode
	t.Run("default mode", func(t *testing.T) {
		t.Parallel()

		config := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: "file",
				Path: flagConfig,
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(config, logger, notifiers, nil, nil)
		require.NoError(t, err, "failed to create FlagsetManager")
		assert.NotNil(t, manager)
		defer manager.Close()

		assert.True(t, manager.IsDefaultFlagSet())
	})

	// Test flagset mode
	t.Run("flagset mode", func(t *testing.T) {
		t.Parallel()

		config := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "test-flagset",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"test-api-key"},
				},
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(config, logger, notifiers, nil, nil)
		require.NoError(t, err, "failed to create FlagsetManager")
		assert.NotNil(t, manager)
		defer manager.Close()

		assert.False(t, manager.IsDefaultFlagSet())
	})
}

func TestFlagsetManager_Close(t *testing.T) {
	t.Parallel()

	flagConfig := "../testdata/semaphore/configuration_flags.yaml"

	// Test default mode
	t.Run("default mode", func(t *testing.T) {
		t.Parallel()

		outputDir := t.TempDir()
		config := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: "file",
				Path: flagConfig,
			},
			Exporter: bufferedFileExporter(outputDir),
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(config, logger, notifiers, nil, nil)
		require.NoError(t, err, "failed to create FlagsetManager")
		assert.NotNil(t, manager)

		_, _ = manager.Default().
			BoolVariation("test-flag-rule-apply", ffcontext.NewEvaluationContext("user-key"), false)
		assertFlushedOnClose(t, manager, outputDir)
	})

	// Test flagset mode
	t.Run("flagset mode", func(t *testing.T) {
		t.Parallel()

		outputDir := t.TempDir()
		config := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "test-flagset",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					Exporter: bufferedFileExporter(outputDir),
					APIKeys:  []string{"test-api-key"},
				},
			},
		}
		logger := slog.New(slog.DiscardHandler)
		notifiers := []notifier.Notifier{}
		manager, err := service.NewFlagsetManager(config, logger, notifiers, nil, nil)
		require.NoError(t, err, "failed to create FlagsetManager")
		assert.NotNil(t, manager)

		flagset, err := manager.FlagSet("test-api-key")
		require.NoError(t, err)
		_, _ = flagset.BoolVariation("test-flag-rule-apply", ffcontext.NewEvaluationContext("user-key"), false)
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
func setupManager(t *testing.T, cfg *config.Config) (service.FlagsetManager, *slogutil.ObservedLogs) {
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

	flagConfig := "../testdata/semaphore/configuration_flags.yaml"

	t.Run("should reject switching from default to flagsets mode", func(t *testing.T) {
		t.Parallel()

		// Create manager in default mode
		cfg := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: "file",
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
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"new-key"},
				},
			},
		}

		assert.NotPanics(t, func() {
			manager.OnConfigChange(newConfig)
		})

		// Verify that the error log was displayed
		assert.Equal(t, 1, logs.Len(), "Expected exactly one error log message")
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
					Name: "test-flagset",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"test-api-key"},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		// Try to switch to default mode
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: "file",
				Path: flagConfig,
			},
		}

		// Should not panic and should reject the change
		assert.NotPanics(t, func() {
			manager.OnConfigChange(newConfig)
		})
		// Verify that the error log was displayed
		assert.Equal(t, 1, logs.Len(), "Expected exactly one error log message")
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
				Kind: "file",
				Path: flagConfig,
			},
			AuthorizedKeys: config.APIKeys{
				Evaluation: []string{"old-key"},
			},
		}
		manager, _ := setupManager(t, cfg)

		// Update AuthorizedKeys
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: "file",
				Path: flagConfig,
			},
			AuthorizedKeys: config.APIKeys{
				Evaluation: []string{"new-key"},
			},
		}

		manager.OnConfigChange(newConfig)

		// Verify the key was updated - ForceReloadAPIKeys resets and reloads
		assert.False(t, cfg.APIKeyExists("old-key"))
		assert.True(t, cfg.APIKeyExists("new-key"))
	})

	t.Run("should update APIKeys in default mode", func(t *testing.T) {
		t.Parallel()

		// Create manager in default mode
		cfg := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: "file",
				Path: flagConfig,
			},
			APIKeys: []string{"old-api-key"}, //nolint:staticcheck // SA1019: the test covers the deprecated setting
		}
		manager, _ := setupManager(t, cfg)

		// Update APIKeys
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: "file",
				Path: flagConfig,
			},
			APIKeys: []string{"new-api-key"}, //nolint:staticcheck // SA1019: the test covers the deprecated setting
		}

		manager.OnConfigChange(newConfig)

		// Verify the key was updated - ForceReloadAPIKeys resets and reloads
		assert.False(t, cfg.APIKeyExists("old-api-key"))
		assert.True(t, cfg.APIKeyExists("new-api-key"))
	})

	t.Run("should update both AuthorizedKeys and APIKeys in default mode", func(t *testing.T) {
		t.Parallel()

		// Create manager in default mode
		cfg := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: "file",
				Path: flagConfig,
			},
			AuthorizedKeys: config.APIKeys{
				Evaluation: []string{"old-authorized-key"},
			},
			APIKeys: []string{"old-api-key"}, //nolint:staticcheck // SA1019: the test covers the deprecated setting
		}
		manager, _ := setupManager(t, cfg)

		// Update both
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: "file",
				Path: flagConfig,
			},
			AuthorizedKeys: config.APIKeys{
				Evaluation: []string{"new-authorized-key"},
			},
			APIKeys: []string{"new-api-key"}, //nolint:staticcheck // SA1019: the test covers the deprecated setting
		}

		manager.OnConfigChange(newConfig)

		// Verify both were updated
		assert.False(t, cfg.APIKeyExists("old-authorized-key"))
		assert.False(t, cfg.APIKeyExists("old-api-key"))
		assert.True(t, cfg.APIKeyExists("new-authorized-key"))
		assert.True(t, cfg.APIKeyExists("new-api-key"))
	})

	t.Run("should reject invalid configuration in default mode - missing retriever", func(t *testing.T) {
		t.Parallel()

		// Create manager in default mode
		cfg := &config.Config{
			FlagSets: []config.FlagSet{},
			Retriever: &retrieverconf.RetrieverConf{
				Kind: "file",
				Path: flagConfig,
			},
			APIKeys: []string{"old-api-key"}, //nolint:staticcheck // SA1019: the test covers the deprecated setting
		}
		// Preload API keys to initialize the internal API key set
		cfg.ForceReloadAPIKeys()
		manager, logs := setupManager(t, cfg)

		// Try to update with invalid config (no retriever)
		newConfig := &config.Config{
			FlagSets:  []config.FlagSet{},
			Retriever: nil,
			APIKeys:   []string{"new-api-key"}, //nolint:staticcheck // SA1019: the test covers the deprecated setting
		}

		assert.NotPanics(t, func() {
			manager.OnConfigChange(newConfig)
		})

		// Verify that the error log was displayed
		assert.Equal(t, 1, logs.Len(), "Expected exactly one error log message")
		logEntry := logs.All()[0]
		assert.Equal(t, slog.LevelError, logEntry.Level)
		assert.Contains(t, logEntry.Message, "the new configuration is invalid, it will not be applied")

		// Verify the original config was not changed
		assert.True(t, cfg.APIKeyExists("old-api-key"))
		assert.False(t, cfg.APIKeyExists("new-api-key"))
	})
}

func TestFlagsetManager_OnConfigChange_ValidationAndKeys(t *testing.T) {
	t.Parallel()

	flagConfig := "../testdata/semaphore/configuration_flags.yaml"

	t.Run("should reject invalid configuration in flagsets mode - flagset with no API keys", func(t *testing.T) {
		t.Parallel()

		// Create manager in flagsets mode
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "test-flagset",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"test-api-key"},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		// Try to update with invalid config (flagset with no API keys)
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "test-flagset",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
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
		assert.Equal(t, 1, logs.Len(), "Expected exactly one error log message")
		logEntry := logs.All()[0]
		assert.Equal(t, slog.LevelError, logEntry.Level)
		assert.Contains(t, logEntry.Message, "the new configuration is invalid, it will not be applied")

		// Verify the original config was not changed
		flagset, err := manager.FlagSet("test-api-key")
		require.NoError(t, err)
		assert.NotNil(t, flagset)
	})

	t.Run("should reject invalid configuration in flagsets mode - duplicate API keys", func(t *testing.T) {
		t.Parallel()

		// Create manager in flagsets mode
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "test-flagset-1",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"api-key-1"},
				},
				{
					Name: "test-flagset-2",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"api-key-2"},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		// Try to update with invalid config (duplicate API keys across flagsets)
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "test-flagset-1",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"duplicate-key"},
				},
				{
					Name: "test-flagset-2",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
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
		assert.Equal(t, 1, logs.Len(), "Expected exactly one error log message")
		logEntry := logs.All()[0]
		assert.Equal(t, slog.LevelError, logEntry.Level)
		assert.Contains(t, logEntry.Message, "the new configuration is invalid, it will not be applied")

		// Verify the original config was not changed
		flagset1, err := manager.FlagSet("api-key-1")
		require.NoError(t, err)
		assert.NotNil(t, flagset1)
		flagset2, err := manager.FlagSet("api-key-2")
		require.NoError(t, err)
		assert.NotNil(t, flagset2)
	})
}

func TestFlagsetManager_OnConfigChange_APIKeys(t *testing.T) {
	t.Parallel()

	flagConfig := "../testdata/semaphore/configuration_flags.yaml"

	t.Run("should successfully update API keys in flagsets mode", func(t *testing.T) {
		t.Parallel()

		// Create manager in flagsets mode
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "test-flagset",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"old-api-key"},
				},
			},
		}
		manager, _ := setupManager(t, cfg)

		// Verify old key works before change
		flagset, err := manager.FlagSet("old-api-key")
		require.NoError(t, err)
		assert.NotNil(t, flagset)

		// Update API key
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "test-flagset",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"new-api-key"},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// Verify the key was updated: the old one is no longer routed, the new one reaches the same flagset
		assertKeyRouting(t, manager, map[string]string{"old-api-key": "", "new-api-key": "test-flagset"})
		_, err = manager.FlagSet("old-api-key")
		require.Error(t, err, "old API key should no longer work")
		flagset, err = manager.FlagSet("new-api-key")
		require.NoError(t, err, "new API key should work")
		assert.NotNil(t, flagset)
	})

	t.Run("should add new API keys to existing flagset", func(t *testing.T) {
		t.Parallel()

		// Create manager in flagsets mode with single key
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "test-flagset",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"api-key-1"},
				},
			},
		}
		manager, _ := setupManager(t, cfg)

		// Verify initial state
		_, err := manager.FlagSet("api-key-1")
		require.NoError(t, err)
		_, err = manager.FlagSet("api-key-2")
		require.Error(t, err, "api-key-2 should not work yet")

		// Add second API key
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "test-flagset",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"api-key-1", "api-key-2"},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// Verify both keys work now
		flagset1, err := manager.FlagSet("api-key-1")
		require.NoError(t, err)
		assert.NotNil(t, flagset1)

		flagset2, err := manager.FlagSet("api-key-2")
		require.NoError(t, err)
		assert.NotNil(t, flagset2)

		// Both should point to the same flagset
		name1, _ := manager.FlagSetName("api-key-1")
		name2, _ := manager.FlagSetName("api-key-2")
		assert.Equal(t, name1, name2)
	})

	t.Run("should remove API keys from flagset", func(t *testing.T) {
		t.Parallel()

		// Create manager with two API keys
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "test-flagset",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"api-key-1", "api-key-2"},
				},
			},
		}
		manager, _ := setupManager(t, cfg)

		// Verify both keys work initially
		_, err := manager.FlagSet("api-key-1")
		require.NoError(t, err)
		_, err = manager.FlagSet("api-key-2")
		require.NoError(t, err)

		// Remove one API key
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "test-flagset",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"api-key-1"},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// api-key-1 still reaches the flagset, api-key-2 is no longer routed
		assertKeyRouting(t, manager, map[string]string{"api-key-1": "test-flagset", "api-key-2": ""})
		flagset, err := manager.FlagSet("api-key-1")
		require.NoError(t, err)
		assert.NotNil(t, flagset)
		_, err = manager.FlagSet("api-key-2")
		assert.Error(t, err, "api-key-2 should no longer work")
	})

	t.Run("should move API key between flagsets", func(t *testing.T) {
		t.Parallel()

		// Create manager with two flagsets
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "flagset-1",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"api-key-1", "api-key-move"},
				},
				{
					Name: "flagset-2",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"api-key-2"},
				},
			},
		}
		manager, _ := setupManager(t, cfg)

		// Verify initial state
		name, err := manager.FlagSetName("api-key-move")
		require.NoError(t, err)
		assert.Equal(t, "flagset-1", name)

		// Move api-key-move from flagset-1 to flagset-2
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "flagset-1",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"api-key-1"},
				},
				{
					Name: "flagset-2",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"api-key-2", "api-key-move"},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// Verify api-key-move now points to flagset-2
		name, err = manager.FlagSetName("api-key-move")
		require.NoError(t, err)
		assert.Equal(t, "flagset-2", name)

		// Verify other keys still work correctly
		name1, _ := manager.FlagSetName("api-key-1")
		assert.Equal(t, "flagset-1", name1)
		name2, _ := manager.FlagSetName("api-key-2")
		assert.Equal(t, "flagset-2", name2)
	})

	t.Run("should rotate API keys across flagsets regardless of processing order", func(t *testing.T) {
		t.Parallel()

		// Cyclic rotation in a single reload: ka: A->B, kb: B->C, kc: C->A.
		// This is order-sensitive with incremental per-flagset updates, so it guards against a
		// regression of the routing rebuild.
		newFlagset := func(name, key string) config.FlagSet {
			return config.FlagSet{
				Name:      name,
				Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
				APIKeys:   []string{key},
			}
		}
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				newFlagset("flagset-a", "ka"),
				newFlagset("flagset-b", "kb"),
				newFlagset("flagset-c", "kc"),
			},
		}
		manager, _ := setupManager(t, cfg)

		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				newFlagset("flagset-a", "kc"),
				newFlagset("flagset-b", "ka"),
				newFlagset("flagset-c", "kb"),
			},
		}
		manager.OnConfigChange(newConfig)

		name, err := manager.FlagSetName("ka")
		require.NoError(t, err)
		assert.Equal(t, "flagset-b", name)
		name, err = manager.FlagSetName("kb")
		require.NoError(t, err)
		assert.Equal(t, "flagset-c", name)
		name, err = manager.FlagSetName("kc")
		require.NoError(t, err)
		assert.Equal(t, "flagset-a", name)
	})
}

func TestFlagsetManager_OnConfigChange_SpecialFlagsets(t *testing.T) {
	t.Parallel()

	flagConfig := "../testdata/semaphore/configuration_flags.yaml"

	t.Run("should not update when config is unchanged in flagsets mode", func(t *testing.T) {
		t.Parallel()

		// Create manager in flagsets mode
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "test-flagset",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"test-api-key"},
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
					Name: "test-flagset",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"test-api-key"},
				},
			},
		}

		manager.OnConfigChange(sameConfig)

		// Verify no "Configuration changed" log was produced
		configChangeLogs := logs.FilterMessage("Configuration changed: updating the APIKeys for flagset")
		assert.Equal(t, 0, configChangeLogs.Len(), "No config change log should be produced when config is unchanged")

		// Verify the key still works
		flagset, err := manager.FlagSet("test-api-key")
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
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"original-key"},
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
		flagset, err := manager.FlagSet("original-key")
		require.NoError(t, err)
		assert.NotNil(t, flagset)

		// Try to update config with empty name flagset - should be ignored
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "", // Still empty
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"new-key"}, // Try to change key
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// Original key should still work because empty name flagsets can't be updated
		flagset, err = manager.FlagSet("original-key")
		require.NoError(t, err)
		assert.NotNil(t, flagset)
	})

	t.Run("should not update API keys for flagset named default", func(t *testing.T) {
		t.Parallel()

		// Create manager with a flagset named "default" (gets auto-generated)
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "default", // Reserved name - will be auto-generated
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"original-key"},
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
		flagset, err := manager.FlagSet("original-key")
		require.NoError(t, err)
		assert.NotNil(t, flagset)

		// Try to update config with "default" name flagset - should be ignored
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "default",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"new-key"},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// Original key should still work because "default" name flagsets can't be updated
		flagset, err = manager.FlagSet("original-key")
		require.NoError(t, err)
		assert.NotNil(t, flagset)
	})

	t.Run("should update multiple flagsets API keys simultaneously", func(t *testing.T) {
		t.Parallel()

		// Create manager with multiple flagsets
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "flagset-1",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"old-key-1"},
				},
				{
					Name: "flagset-2",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"old-key-2"},
				},
			},
		}
		manager, _ := setupManager(t, cfg)

		// Verify initial state
		name1, _ := manager.FlagSetName("old-key-1")
		assert.Equal(t, "flagset-1", name1)
		name2, _ := manager.FlagSetName("old-key-2")
		assert.Equal(t, "flagset-2", name2)

		// Update both flagsets
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name: "flagset-1",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"new-key-1"},
				},
				{
					Name: "flagset-2",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: flagConfig,
					},
					APIKeys: []string{"new-key-2"},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// Verify old keys no longer work
		_, err := manager.FlagSet("old-key-1")
		require.Error(t, err)
		_, err = manager.FlagSet("old-key-2")
		require.Error(t, err)

		// Verify new keys work and point to correct flagsets
		name1, err = manager.FlagSetName("new-key-1")
		require.NoError(t, err)
		assert.Equal(t, "flagset-1", name1)

		name2, err = manager.FlagSetName("new-key-2")
		require.NoError(t, err)
		assert.Equal(t, "flagset-2", name2)
	})

	t.Run("should only notify the websocket clients of the flagset added at runtime", func(t *testing.T) {
		t.Parallel()

		flagset := func(name, apiKey string) config.FlagSet {
			return config.FlagSet{
				Name:      name,
				Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
				APIKeys:   []string{apiKey},
			}
		}
		existing := flagset("flagset-1", "key-1")
		// flagset-1 never notifies, so its client must not receive anything.
		existing.DisableNotifierOnInit = true

		wsService := stream.NewWebsocketService()
		manager, err := service.NewFlagsetManager(
			&config.Config{FlagSets: []config.FlagSet{existing}}, slog.New(slog.DiscardHandler), nil, nil, wsService)
		require.NoError(t, err)
		t.Cleanup(func() { manager.Close() })

		conn1, conn2 := &countingWSConn{}, &countingWSConn{}
		wsService.Register(conn1, "flagset-1")
		wsService.Register(conn2, "flagset-2")

		manager.OnConfigChange(&config.Config{
			FlagSets: []config.FlagSet{existing, flagset("flagset-2", "key-2")},
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

	flagConfig := "../testdata/semaphore/configuration_flags.yaml"

	t.Run("should add a new flagset at runtime", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      "flagset-1",
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					APIKeys:   []string{"key-1"},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		// The second flagset does not exist yet.
		_, err := manager.FlagSet("key-2")
		require.Error(t, err)

		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      "flagset-1",
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					APIKeys:   []string{"key-1"},
				},
				{
					Name:      "flagset-2",
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					APIKeys:   []string{"key-2"},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// The new flagset is now available and the existing one still works.
		flagset2, err := manager.FlagSet("key-2")
		require.NoError(t, err, "the flagset added at runtime should be available")
		assert.NotNil(t, flagset2)
		name2, err := manager.FlagSetName("key-2")
		require.NoError(t, err)
		assert.Equal(t, "flagset-2", name2)

		flagset1, err := manager.FlagSet("key-1")
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
					Name:      "flagset-1",
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					APIKeys:   []string{"key-1"},
				},
				{
					Name:      "flagset-2",
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					APIKeys:   []string{"key-2"},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		// Both flagsets exist initially.
		_, err := manager.FlagSet("key-1")
		require.NoError(t, err)
		_, err = manager.FlagSet("key-2")
		require.NoError(t, err)

		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      "flagset-1",
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					APIKeys:   []string{"key-1"},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// flagset-2 is gone, flagset-1 still works.
		flagset1, err := manager.FlagSet("key-1")
		require.NoError(t, err)
		assert.NotNil(t, flagset1)

		_, err = manager.FlagSet("key-2")
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
					Name:            "flagset-1",
					Retriever:       &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					PollingInterval: 0,
					APIKeys:         []string{"key-1"},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		// Change the polling interval (a non-API-key field) -> forbidden.
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:            "flagset-1",
					Retriever:       &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					PollingInterval: 5000,
					APIKeys:         []string{"key-1"},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// The flagset is still served (unchanged) and an error is logged.
		flagset, err := manager.FlagSet("key-1")
		require.NoError(t, err)
		assert.NotNil(t, flagset)

		rejectedLogs := logs.FilterMessageSnippet("modifying a flagset is not supported at runtime")
		assert.Equal(t, 1, rejectedLogs.Len(), "expected an error log about the rejected modification")
		assert.Contains(t, rejectedLogs.All()[0].Message, "next time the relay proxy is restarted")
		assert.Equal(t, "flagset-1", rejectedLogs.All()[0].ContextMap()["flagset"])
		// No bundled API-key error since the API keys did not change.
		assert.Equal(t, 0, logs.FilterMessageSnippet("API keys change for this flagset is also ignored").Len())
	})
}

func TestFlagsetManager_OnConfigChange_FlagsetModifications(t *testing.T) {
	t.Parallel()

	flagConfig := "../testdata/semaphore/configuration_flags.yaml"

	t.Run("should reject a bundled API key and configuration change", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:            "flagset-1",
					Retriever:       &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					PollingInterval: 0,
					APIKeys:         []string{"old-key"},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		// Change both the polling interval and the API key -> the whole change is rejected.
		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:            "flagset-1",
					Retriever:       &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					PollingInterval: 5000,
					APIKeys:         []string{"new-key"},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// The old key still works and the new key is NOT applied.
		flagset, err := manager.FlagSet("old-key")
		require.NoError(t, err, "the old API key must still work because the change is rejected")
		assert.NotNil(t, flagset)
		_, err = manager.FlagSet("new-key")
		require.Error(t, err, "the bundled API key change must not be applied")

		// Two error logs: the modification rejection and the explicit bundled-key rejection.
		assert.Equal(t, 1, logs.FilterMessageSnippet("modifying a flagset is not supported at runtime").Len())
		assert.Equal(t, 1, logs.FilterMessageSnippet("API keys change for this flagset is also ignored").Len())
	})

	t.Run("should apply add, remove and key change while rejecting a modification in one reload", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      "flagset-a",
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					APIKeys:   []string{"key-a"},
				},
				{
					Name:      "flagset-b",
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					APIKeys:   []string{"key-b"},
				},
				{
					Name:            "flagset-c",
					Retriever:       &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					PollingInterval: 0,
					APIKeys:         []string{"key-c"},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				// flagset-a: unchanged
				{
					Name:      "flagset-a",
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					APIKeys:   []string{"key-a"},
				},
				// flagset-b: removed (absent)
				// flagset-c: forbidden modification (polling interval changed)
				{
					Name:            "flagset-c",
					Retriever:       &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					PollingInterval: 5000,
					APIKeys:         []string{"key-c"},
				},
				// flagset-d: added
				{
					Name:      "flagset-d",
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					APIKeys:   []string{"key-d"},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// a unchanged, b removed, c kept (modification rejected), d added.
		_, err := manager.FlagSet("key-a")
		require.NoError(t, err, "flagset-a should be unchanged")
		_, err = manager.FlagSet("key-b")
		require.Error(t, err, "flagset-b should have been removed")
		_, err = manager.FlagSet("key-c")
		require.NoError(t, err, "flagset-c should be kept because its modification was rejected")
		_, err = manager.FlagSet("key-d")
		require.NoError(t, err, "flagset-d should have been added")

		all, err := manager.AllFlagSets()
		require.NoError(t, err)
		assert.Len(t, all, 3, "expected flagset-a, flagset-c and flagset-d")

		// Only flagset-c's modification is rejected.
		assert.Equal(t, 1, logs.FilterMessageSnippet("modifying a flagset is not supported at runtime").Len())
	})

	t.Run("should leave unnamed flagsets untouched and warn when their count changes", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      "flagset-named",
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					APIKeys:   []string{"named-key"},
				},
				{
					Name:      "", // unnamed -> gets a generated name at startup
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
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
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
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

	flagConfig := "../testdata/semaphore/configuration_flags.yaml"

	t.Run("should fail closed when an accepted key move collides with a rejected modification", func(t *testing.T) {
		t.Parallel()

		// fs-a gains "k-move" (accepted) while fs-b — which still holds "k-move" — also gets a
		// forbidden retriever change (rejected, so it keeps its old keys). The reconciled config
		// then has "k-move" on BOTH flagsets even though the incoming config was valid. The key
		// must not route non-deterministically: it is left unrouted (fail closed).
		otherFlagConfig := "../../../testdata/flag-config.yaml"
		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      "fs-a",
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					APIKeys:   []string{"k-a"},
				},
				{
					Name:      "fs-b",
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					APIKeys:   []string{"k-b", "k-move"},
				},
			},
		}
		manager, logs := setupManager(t, cfg)

		newConfig := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      "fs-a",
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					APIKeys:   []string{"k-a", "k-move"}, // gains k-move (accepted)
				},
				{
					Name: "fs-b",
					// Forbidden retriever change -> the whole fs-b change is rejected, so it
					// keeps its old keys [k-b, k-move].
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: otherFlagConfig},
					APIKeys:   []string{"k-b"},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// Non-colliding keys still route deterministically.
		_, err := manager.FlagSet("k-a")
		require.NoError(t, err)
		_, err = manager.FlagSet("k-b")
		require.NoError(t, err)
		// The colliding key is failed closed: it routes to no flagset.
		_, err = manager.FlagSet("k-move")
		require.Error(t, err, "a key present on two flagsets after reconciliation must not route")

		assert.GreaterOrEqual(t,
			logs.FilterMessageSnippet("configured on multiple flagsets after reconciliation").Len(), 1,
			"expected an error log about the colliding API key")
		assert.GreaterOrEqual(t,
			logs.FilterMessageSnippet("modifying a flagset is not supported at runtime").Len(), 1,
			"expected the forbidden modification of fs-b to be rejected")
	})

	t.Run("should skip a flagset added at runtime whose client cannot be created", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:      "fs-keep",
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					APIKeys:   []string{"keep-key"},
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
					Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
					APIKeys:   []string{"keep-key"},
				},
				{
					Name: "fs-broken",
					Retriever: &retrieverconf.RetrieverConf{
						Kind: "file",
						Path: "/does/not/exist/flags.yaml",
					},
					APIKeys: []string{"broken-key"},
				},
			},
		}

		manager.OnConfigChange(newConfig)

		// The broken flagset is not registered; the existing flagset keeps working.
		_, err := manager.FlagSet("broken-key")
		require.Error(t, err, "a flagset whose client failed to build must not be reachable")
		_, err = manager.FlagSet("keep-key")
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

	flagConfig := "../../../testdata/flag-config.yaml"
	stable := config.FlagSet{
		Name:      "fs-stable",
		Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
		APIKeys:   []string{"stable-key"},
	}
	churn := config.FlagSet{
		Name:      "fs-churn",
		Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
		APIKeys:   []string{"churn-key"},
	}
	manager, _ := setupManager(t, &config.Config{FlagSets: []config.FlagSet{stable, churn}})

	withChurn := &config.Config{FlagSets: []config.FlagSet{stable, churn}}
	withoutChurn := &config.Config{FlagSets: []config.FlagSet{stable}}

	done := make(chan struct{})
	var stableErrors atomic.Int64
	var wg sync.WaitGroup

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

	flagConfig := "../../../testdata/flag-config.yaml"

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
				Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
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
				Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
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
	_, err = client.RawVariation("test-flag", ffcontext.NewEvaluationContext("user-key"), false)
	require.NoError(t, err)
	require.Equal(t, int64(0), exportCalls.Load(),
		"the event must still be buffered before the flagset is removed")

	// Remove the flagset at runtime (keeping flagset-keep so the mode does not change).
	newConfig := &config.Config{
		FlagSets: []config.FlagSet{
			{
				Name:      "flagset-keep",
				Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: flagConfig},
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
