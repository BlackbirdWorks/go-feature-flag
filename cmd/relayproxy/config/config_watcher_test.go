package config_test

import (
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/testhelpers"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func createTestConfig(t *testing.T, configContent string) (*config.Config, *os.File) {
	t.Helper()
	configFile := testhelpers.CopyContentToNewTempFile(t, configContent)
	f := pflag.NewFlagSet(fixtures.Config, pflag.ContinueOnError)
	f.String(fixtures.Config, "", locationOfYourConfig)
	err := f.Parse([]string{configFlag + configFile.Name()})
	require.NoError(t, err)

	cfg, err := config.New(f, slog.New(slog.DiscardHandler), fixtures.VersionOneZeroZero)
	require.NoError(t, err)

	return cfg, configFile
}

func TestConfigAttachConfigChangeCallback(t *testing.T) {
	t.Parallel()

	initialConfig := serverPort1031PollingInterval

	updatedConfig := `server:
  port: 2031
pollingInterval: 2000
loglevel: debug`

	cfg, configFile := createTestConfig(t, initialConfig)

	var (
		mu             sync.Mutex
		callbackCalled bool
		receivedConfig *config.Config
	)

	callback := func(newConfig *config.Config) {
		mu.Lock()
		defer mu.Unlock()

		callbackCalled = true
		receivedConfig = newConfig
	}

	cfg.AttachConfigChangeCallback(callback)

	// Update config file
	_ = testhelpers.CopyContentToExistingTempFile(t, updatedConfig, configFile)

	// Wait for callback to be triggered
	timeout := time.After(5 * time.Second)

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			require.Fail(t, timeoutWaitingForCallback)
		case <-ticker.C:
			mu.Lock()
			if callbackCalled {
				mu.Unlock()
				assert.NotNil(t, receivedConfig, "Received config should not be nil")
				assert.Equal(t, 2031, receivedConfig.Server.Port, "Port should be updated")
				assert.Equal(t, 2000, receivedConfig.PollingInterval, "PollingInterval should be updated")
				assert.Equal(t, fixtures.Debug, receivedConfig.LogLevel, "LogLevel should be updated")

				err := cfg.StopConfigChangeWatcher()
				assert.NoError(t, err)

				return
			}
			mu.Unlock()
		}
	}
}

func TestConfigStopConfigChangeWatcher(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr     error
		setupConfig func(*testing.T) *config.Config
		name        string
	}{
		{
			name: "Successfully stop watcher when configLoader is initialized",
			setupConfig: func(t *testing.T) *config.Config {
				t.Helper()
				cfg, _ := createTestConfig(t, serverPort1031PollingInterval)

				return cfg
			},
			wantErr: nil,
		},
		{
			name: "Stop watcher when configLoader is nil - should return nil",
			setupConfig: func(t *testing.T) *config.Config {
				t.Helper()

				return &config.Config{}
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := tt.setupConfig(t)
			err := cfg.StopConfigChangeWatcher()

			assert.Equal(t, tt.wantErr, err)
		})
	}
}

func TestConfigAttachConfigChangeCallbackMultipleCallbacks(t *testing.T) {
	t.Parallel()

	initialConfig := serverPort1031PollingInterval

	updatedConfig := `server:
  port: 9999
pollingInterval: 5000
loglevel: error`

	cfg, configFile := createTestConfig(t, initialConfig)

	var (
		mu                               sync.Mutex
		callback1Called, callback2Called bool
	)

	callback1 := func(_ *config.Config) {
		mu.Lock()
		defer mu.Unlock()

		callback1Called = true
	}

	callback2 := func(_ *config.Config) {
		mu.Lock()
		defer mu.Unlock()

		callback2Called = true
	}

	cfg.AttachConfigChangeCallback(callback1)
	cfg.AttachConfigChangeCallback(callback2)

	_ = testhelpers.CopyContentToExistingTempFile(t, updatedConfig, configFile)

	timeout := time.After(5 * time.Second)

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			require.Fail(t, "Timeout waiting for callbacks to be triggered")
		case <-ticker.C:
			mu.Lock()
			if callback1Called && callback2Called {
				mu.Unlock()
				assert.True(t, callback1Called, "First callback should be called")
				assert.True(t, callback2Called, "Second callback should be called")

				err := cfg.StopConfigChangeWatcher()
				assert.NoError(t, err)

				return
			}
			mu.Unlock()
		}
	}
}
