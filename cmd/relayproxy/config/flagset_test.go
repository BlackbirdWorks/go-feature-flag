package config_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
)

const (
	flagsetNonExistingNot = "flagset non-existing not found"
	key1                  = "key-1"
	key2                  = "key-2"
	key3                  = "key-3"
	nonExisting           = "non-existing"
)

const (
	flagset1Name   = flagset1
	flagset2Name   = flagset2
	flagset3Name   = "flagset-3"
	oldKey1        = "old-key-1"
	oldKey2        = "old-key-2"
	flagsetNameFmt = "flagset-%d"
)

func TestConfigSetFlagSetAPIKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		config         *config.Config
		name           string
		flagsetName    string
		wantErrContain string
		apiKeys        []string
		wantErr        bool
	}{
		{
			name: "set API keys for existing flagset",
			config: &config.Config{
				FlagSets: []config.FlagSet{
					{Name: flagset1Name, APIKeys: []string{oldKey1}},
					{Name: flagset2Name, APIKeys: []string{oldKey2}},
				},
			},
			flagsetName: flagset1Name,
			apiKeys:     []string{"new-key-1", "new-key-2"},
			wantErr:     false,
		},
		{
			name: "set empty API keys for existing flagset",
			config: &config.Config{
				FlagSets: []config.FlagSet{
					{Name: flagset1Name, APIKeys: []string{oldKey1}},
				},
			},
			flagsetName: flagset1Name,
			apiKeys:     []string{},
			wantErr:     false,
		},
		{
			name: "set API keys for non-existing flagset",
			config: &config.Config{
				FlagSets: []config.FlagSet{
					{Name: flagset1Name, APIKeys: []string{oldKey1}},
				},
			},
			flagsetName:    nonExisting,
			apiKeys:        []string{"new-key"},
			wantErr:        true,
			wantErrContain: flagsetNonExistingNot,
		},
		{
			name: "set API keys with empty flagsets config",
			config: &config.Config{
				FlagSets: []config.FlagSet{},
			},
			flagsetName:    flagset1Name,
			apiKeys:        []string{"new-key"},
			wantErr:        true,
			wantErrContain: "flagset flagset-1 not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.config.SetFlagSetAPIKeys(tt.flagsetName, tt.apiKeys)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContain)
			} else {
				require.NoError(t, err)
				// Verify the API keys were actually set
				gotKeys, getFlagSetAPIKeysErr := tt.config.GetFlagSetAPIKeys(tt.flagsetName)
				require.NoError(t, getFlagSetAPIKeysErr)
				assert.Equal(t, tt.apiKeys, gotKeys)
			}
		})
	}
}

func TestConfigGetFlagSetAPIKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		config         *config.Config
		name           string
		flagsetName    string
		wantErrContain string
		wantAPIKeys    []string
		wantErr        bool
	}{
		{
			name: "get API keys for existing flagset",
			config: &config.Config{
				FlagSets: []config.FlagSet{
					{Name: flagset1Name, APIKeys: []string{key1, key2}},
					{Name: flagset2Name, APIKeys: []string{key3}},
				},
			},
			flagsetName: flagset1Name,
			wantAPIKeys: []string{key1, key2},
			wantErr:     false,
		},
		{
			name: "get API keys for flagset with empty keys",
			config: &config.Config{
				FlagSets: []config.FlagSet{
					{Name: flagset1Name, APIKeys: []string{}},
				},
			},
			flagsetName: flagset1Name,
			wantAPIKeys: []string{},
			wantErr:     false,
		},
		{
			name: "get API keys for flagset with nil keys",
			config: &config.Config{
				FlagSets: []config.FlagSet{
					{Name: flagset1Name, APIKeys: nil},
				},
			},
			flagsetName: flagset1Name,
			wantAPIKeys: nil,
			wantErr:     false,
		},
		{
			name: "get API keys for non-existing flagset",
			config: &config.Config{
				FlagSets: []config.FlagSet{
					{Name: flagset1Name, APIKeys: []string{key1}},
				},
			},
			flagsetName:    nonExisting,
			wantAPIKeys:    nil,
			wantErr:        true,
			wantErrContain: flagsetNonExistingNot,
		},
		{
			name: "get API keys with empty flagsets config",
			config: &config.Config{
				FlagSets: []config.FlagSet{},
			},
			flagsetName:    flagset1Name,
			wantAPIKeys:    nil,
			wantErr:        true,
			wantErrContain: "flagset flagset-1 not found",
		},
		{
			name: "get API keys for second flagset in list",
			config: &config.Config{
				FlagSets: []config.FlagSet{
					{Name: flagset1Name, APIKeys: []string{key1}},
					{Name: flagset2Name, APIKeys: []string{key2, key3}},
					{Name: flagset3Name, APIKeys: []string{"key-4"}},
				},
			},
			flagsetName: flagset2Name,
			wantAPIKeys: []string{key2, key3},
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotAPIKeys, err := tt.config.GetFlagSetAPIKeys(tt.flagsetName)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContain)
				assert.Nil(t, gotAPIKeys)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantAPIKeys, gotAPIKeys)
			}
		})
	}
}

func TestConfigGetFlagSets(t *testing.T) {
	t.Parallel()

	t.Run("returns all configured flagsets", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{Name: flagset1Name, APIKeys: []string{key1}},
				{Name: flagset2Name, APIKeys: []string{key2}},
			},
		}
		got := cfg.GetFlagSets()
		require.Len(t, got, 2)
		assert.Equal(t, flagset1Name, got[0].Name)
		assert.Equal(t, flagset2Name, got[1].Name)
	})

	t.Run("returns an empty slice when no flagset is configured", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{FlagSets: []config.FlagSet{}}
		assert.Empty(t, cfg.GetFlagSets())
	})

	t.Run("returns a copy that is independent of the configuration", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{Name: flagset1Name, APIKeys: []string{key1}},
			},
		}
		got := cfg.GetFlagSets()
		// Mutating the returned slice (rename, append) must not affect the config.
		got[0].Name = "mutated"
		got = append(got, config.FlagSet{Name: "extra"})
		_ = got

		fresh := cfg.GetFlagSets()
		require.Len(t, fresh, 1)
		assert.Equal(t, flagset1Name, fresh[0].Name)
	})
}

func TestConfigAddFlagSet(t *testing.T) {
	t.Parallel()

	t.Run("adds a new flagset", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{Name: flagset1Name, APIKeys: []string{key1}},
			},
		}
		err := cfg.AddFlagSet(config.FlagSet{Name: flagset2Name, APIKeys: []string{key2}})
		require.NoError(t, err)

		got := cfg.GetFlagSets()
		require.Len(t, got, 2)

		keys, err := cfg.GetFlagSetAPIKeys(flagset2Name)
		require.NoError(t, err)
		assert.Equal(t, []string{key2}, keys)
	})

	t.Run("returns an error when the flagset already exists", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{Name: flagset1Name, APIKeys: []string{key1}},
			},
		}
		err := cfg.AddFlagSet(config.FlagSet{Name: flagset1Name, APIKeys: []string{key2}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "flagset flagset-1 already exists")
		// The existing flagset must be untouched.
		assert.Len(t, cfg.GetFlagSets(), 1)
	})
}

func TestConfigRemoveFlagSet(t *testing.T) {
	t.Parallel()

	t.Run("removes an existing flagset", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{Name: flagset1Name, APIKeys: []string{key1}},
				{Name: flagset2Name, APIKeys: []string{key2}},
			},
		}
		err := cfg.RemoveFlagSet(flagset1Name)
		require.NoError(t, err)

		got := cfg.GetFlagSets()
		require.Len(t, got, 1)
		assert.Equal(t, flagset2Name, got[0].Name)

		_, err = cfg.GetFlagSetAPIKeys(flagset1Name)
		assert.Error(t, err)
	})

	t.Run("returns an error when the flagset does not exist", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			FlagSets: []config.FlagSet{
				{Name: flagset1Name, APIKeys: []string{key1}},
			},
		}
		err := cfg.RemoveFlagSet(nonExisting)
		require.Error(t, err)
		assert.Contains(t, err.Error(), flagsetNonExistingNot)
		assert.Len(t, cfg.GetFlagSets(), 1)
	})
}

func TestFlagSetAPIKeysConcurrency(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		FlagSets: []config.FlagSet{
			{Name: flagset1Name, APIKeys: []string{"initial-key-1"}},
			{Name: flagset2Name, APIKeys: []string{"initial-key-2"}},
			{Name: flagset3Name, APIKeys: []string{"initial-key-3"}},
		},
	}

	const (
		numGoroutines = 100
		numIterations = 50
	)

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // readers and writers

	// Start writer goroutines
	for i := range numGoroutines {
		go func(id int) {
			defer wg.Done()

			for j := range numIterations {
				flagsetName := fmt.Sprintf(flagsetNameFmt, (id%3)+1)
				newKeys := []string{fmt.Sprintf("key-%d-%d", id, j)}
				err := cfg.SetFlagSetAPIKeys(flagsetName, newKeys)
				assert.NoError(t, err) // not require: this runs outside the test goroutine
			}
		}(i)
	}

	// Start reader goroutines
	for i := range numGoroutines {
		go func(id int) {
			defer wg.Done()

			for range numIterations {
				flagsetName := fmt.Sprintf(flagsetNameFmt, (id%3)+1)
				keys, err := cfg.GetFlagSetAPIKeys(flagsetName)
				// We don't check the exact value since it may change between reads
				// but we verify the operation doesn't panic or return an error
				assert.NoError(t, err)
				assert.NotNil(t, keys)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Verify final state - each flagset should have exactly one key (from the last write)
	for i := 1; i <= 3; i++ {
		flagsetName := fmt.Sprintf(flagsetNameFmt, i)
		keys, err := cfg.GetFlagSetAPIKeys(flagsetName)
		require.NoError(t, err)
		assert.Len(t, keys, 1, "flagset %s should have exactly one key after concurrent writes", flagsetName)
	}
}
