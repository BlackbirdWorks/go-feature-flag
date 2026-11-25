package service_test

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	expectedEmptyInfoResponse       = "Expected empty InfoResponse"
	expectedLatestCacheRefreshToNot = "Expected LatestCacheRefresh to not be nil"
	expectedNoError                 = "Expected no error"
)

var (
	errFailedToGetFlagsets = errors.New("failed to get flagsets")
)

func TestWithDefaultMode(t *testing.T) {
	t.Parallel()

	t.Run("health", func(t *testing.T) {
		t.Parallel()

		manager, err := service.NewFlagsetManager(&config.Config{
			PollingInterval: 60000, // 1 minute in milliseconds
			FileFormat:      protocol.FormatYAML,
			Retrievers: &[]retrieverconf.RetrieverConf{
				{Kind: fixtures.File, Path: fixtures.FlagConfigYAMLParentPath},
			},
		}, slog.New(slog.DiscardHandler), nil, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, manager)

		monitoring := service.NewMonitoring(manager)
		health := monitoring.Health()
		assert.True(t, health.Initialized, "Expected initialized to be true, got false")
	})

	t.Run("info", func(t *testing.T) {
		t.Parallel()

		manager, err := service.NewFlagsetManager(&config.Config{
			PollingInterval: 60000, // 1 minute in milliseconds
			FileFormat:      protocol.FormatYAML,
			Retrievers: &[]retrieverconf.RetrieverConf{
				{Kind: fixtures.File, Path: fixtures.FlagConfigYAMLParentPath},
			},
		}, slog.New(slog.DiscardHandler), nil, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, manager)

		monitoring := service.NewMonitoring(manager)
		info, err := monitoring.Info()
		require.NoError(t, err, expectedNoError)
		assert.NotNil(t, info.LatestCacheRefresh, expectedLatestCacheRefreshToNot)
		assert.False(
			t,
			info.LatestCacheRefresh.IsZero(),
			"Expected LatestCacheRefresh to not be zero, got zero",
		)
	})

	t.Run("info with error", func(t *testing.T) {
		t.Parallel()

		manager, err := service.NewFlagsetManager(&config.Config{
			PollingInterval: 60000, // 1 minute in milliseconds
			FileFormat:      protocol.FormatYAML,
			Retrievers: &[]retrieverconf.RetrieverConf{
				{Kind: fixtures.File, Path: "../../../testdata/non-existent-file.yaml"},
			},
		}, slog.New(slog.DiscardHandler), nil, nil, nil)
		require.Error(t, err, "Expected error due to invalid file path")
		assert.Nil(t, manager)
	})

	t.Run("with nil config", func(t *testing.T) {
		t.Parallel()

		manager, err := service.NewFlagsetManager(nil, slog.New(slog.DiscardHandler), nil, nil, nil)
		require.Error(t, err, "Expected error when config is nil")
		assert.Nil(t, manager, "Expected manager to be nil")
	})

	t.Run("is default flagset", func(t *testing.T) {
		t.Parallel()

		managerDefault, err := service.NewFlagsetManager(&config.Config{
			PollingInterval: 60000, // 1 minute in milliseconds
			FileFormat:      protocol.FormatYAML,
			Retrievers: &[]retrieverconf.RetrieverConf{
				{Kind: fixtures.File, Path: fixtures.FlagConfigYAMLParentPath},
			},
		}, slog.New(slog.DiscardHandler), nil, nil, nil)
		require.NoError(t, err)
		assert.True(t, managerDefault.IsDefaultFlagSet())
	})
}

func TestWithFlagsetsMode(t *testing.T) {
	t.Parallel()

	t.Run("info", func(t *testing.T) {
		t.Parallel()

		manager, err := service.NewFlagsetManager(&config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:            fixtures.FlagsetTest,
					PollingInterval: 60000, // 1 minute in milliseconds
					FileFormat:      protocol.FormatYAML,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{Kind: fixtures.File, Path: fixtures.FlagConfigYAMLParentPath},
					},
					APIKeys: []string{fixtures.APIKeyTest},
				},
			},
		}, slog.New(slog.DiscardHandler), nil, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, manager)

		monitoring := service.NewMonitoring(manager)
		info, err := monitoring.Info()
		require.NoError(t, err, expectedNoError)
		assert.NotEmpty(t, info.Flagsets, "Expected flagsets to not be empty")
		assert.Contains(t, info.Flagsets, fixtures.FlagsetTest, "Expected test-flagset to be in flagsets")
	})

	t.Run("info multiple flagsets", func(t *testing.T) {
		t.Parallel()

		manager, err := service.NewFlagsetManager(&config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:            "flagset1",
					PollingInterval: 60000, // 1 minute in milliseconds
					FileFormat:      protocol.FormatYAML,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{Kind: fixtures.File, Path: fixtures.FlagConfigYAMLParentPath},
					},
					APIKeys: []string{apiKeyOne},
				},
				{
					Name:            "flagset2",
					PollingInterval: 60000, // 1 minute in milliseconds
					FileFormat:      protocol.FormatYAML,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{Kind: fixtures.File, Path: "../../../testdata/flag-config-2nd-file.yaml"},
					},
					APIKeys: []string{apiKeyTwo},
				},
			},
		}, slog.New(slog.DiscardHandler), nil, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, manager)

		monitoring := service.NewMonitoring(manager)
		info, err := monitoring.Info()
		require.NoError(t, err, expectedNoError)
		assert.Len(t, info.Flagsets, 2, "Expected 2 flagsets")
		assert.Contains(t, info.Flagsets, "flagset1", "Expected flagset1 to be in flagsets")
		assert.Contains(t, info.Flagsets, "flagset2", "Expected flagset2 to be in flagsets")
	})

	t.Run("is default flagset", func(t *testing.T) {
		t.Parallel()

		managerFlagsets, err := service.NewFlagsetManager(&config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:            fixtures.FlagsetTest,
					PollingInterval: 60000, // 1 minute in milliseconds
					FileFormat:      protocol.FormatYAML,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{Kind: fixtures.File, Path: fixtures.FlagConfigYAMLParentPath},
					},
					APIKeys: []string{fixtures.APIKeyTest},
				},
			},
		}, slog.New(slog.DiscardHandler), nil, nil, nil)
		require.NoError(t, err)
		assert.False(t, managerFlagsets.IsDefaultFlagSet())
	})

	t.Run("empty flagsets", func(t *testing.T) {
		t.Parallel()

		manager, err := service.NewFlagsetManager(&config.Config{
			FlagSets: []config.FlagSet{}, // Empty flagsets
		}, slog.New(slog.DiscardHandler), nil, nil, nil)
		require.Error(t, err, "Expected error due to no retrievers configured")
		assert.Nil(t, manager)
	})

	t.Run("all flagsets fail to initialize", func(t *testing.T) {
		t.Parallel()

		manager, err := service.NewFlagsetManager(&config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:            "invalid-flagset",
					PollingInterval: 60000, // 1 minute in milliseconds
					FileFormat:      protocol.FormatYAML,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{Kind: fixtures.File, Path: "../../../testdata/non-existent-file.yaml"},
					},
					APIKeys: []string{apiKeyOne},
				},
			},
		}, slog.New(slog.DiscardHandler), nil, nil, nil)
		require.Error(t, err, "Expected error due to invalid file path")
		assert.Nil(t, manager)
	})
}

// TestMonitoringInfoErrors tests the error scenarios in the Info() function.
func TestMonitoringInfoErrors(t *testing.T) {
	t.Parallel()

	t.Run("nil flagsetManager should return error", func(t *testing.T) {
		t.Parallel()

		monitoring := service.NewMonitoring(nil)
		info, err := monitoring.Info()
		require.Error(t, err, "Expected error when flagsetManager is nil")
		assert.Equal(t, "flagset manager is not initialized", err.Error())
		assert.Equal(t, model.InfoResponse{}, info, expectedEmptyInfoResponse)
	})

	t.Run("FlagSets error should be propagated", func(t *testing.T) {
		t.Parallel()

		mockManager := &mock.MockFlagsetManager{
			FlagSets:       nil,
			DefaultFlagSet: nil,
			GetFlagSetsErr: errFailedToGetFlagsets,
		}
		monitoring := service.NewMonitoring(mockManager)
		info, err := monitoring.Info()
		require.Error(t, err, "Expected error from FlagSets")
		assert.Equal(t, "listing the flag sets for the information: failed to get flagsets", err.Error())
		assert.Equal(t, model.InfoResponse{}, info, expectedEmptyInfoResponse)
	})

	t.Run("default mode with nil default flagset should return error", func(t *testing.T) {
		t.Parallel()

		mockManager := &mock.MockFlagsetManager{
			FlagSets:            map[string]*ffclient.GoFeatureFlag{},
			DefaultFlagSet:      nil,
			IsDefaultFlagSeItem: true,
			GetFlagSetsErr:      nil,
		}
		monitoring := service.NewMonitoring(mockManager)
		info, err := monitoring.Info()
		require.Error(t, err, "Expected error when default flagset is nil")
		assert.Equal(t, "no default flagset configured", err.Error())
		assert.Equal(t, model.InfoResponse{}, info, expectedEmptyInfoResponse)
	})

	t.Run("default mode with valid default flagset should succeed", func(t *testing.T) {
		t.Parallel()

		// We need to mock the GetCacheRefreshDate method, but since it's not easily mockable,
		// we'll test the happy path with a real manager instead

		manager, err := service.NewFlagsetManager(&config.Config{
			PollingInterval: 60000,
			FileFormat:      protocol.FormatYAML,
			Retrievers: &[]retrieverconf.RetrieverConf{
				{Kind: fixtures.File, Path: fixtures.FlagConfigYAMLParentPath},
			},
		}, slog.New(slog.DiscardHandler), nil, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, manager)

		monitoring := service.NewMonitoring(manager)
		info, err := monitoring.Info()
		require.NoError(t, err, "Expected no error with valid default flagset")
		assert.NotNil(t, info.LatestCacheRefresh, expectedLatestCacheRefreshToNot)
		assert.False(t, info.LatestCacheRefresh.IsZero(), "Expected LatestCacheRefresh to not be zero")
	})

	t.Run("flagsets mode with valid flagsets should succeed", func(t *testing.T) {
		t.Parallel()

		// Since we can't easily mock GetCacheRefreshDate, we'll test with real manager
		manager, err := service.NewFlagsetManager(&config.Config{
			FlagSets: []config.FlagSet{
				{
					Name:            testFlagsetOne,
					PollingInterval: 60000,
					FileFormat:      protocol.FormatYAML,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{Kind: fixtures.File, Path: fixtures.FlagConfigYAMLParentPath},
					},
					APIKeys: []string{apiKeyOne},
				},
				{
					Name:            testFlagsetTwo,
					PollingInterval: 60000,
					FileFormat:      protocol.FormatYAML,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{Kind: fixtures.File, Path: "../../../testdata/flag-config-2nd-file.yaml"},
					},
					APIKeys: []string{apiKeyTwo},
				},
			},
		}, slog.New(slog.DiscardHandler), nil, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, manager)

		monitoring := service.NewMonitoring(manager)
		info, err := monitoring.Info()
		require.NoError(t, err, "Expected no error with valid flagsets")
		assert.NotEmpty(t, info.Flagsets, "Expected flagsets to not be empty")
		assert.NotNil(t, info.LatestCacheRefresh, expectedLatestCacheRefreshToNot)
		assert.False(t, info.LatestCacheRefresh.IsZero(), "Expected LatestCacheRefresh to not be zero")
	})
}
