package ffclient_test

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/exporter/fileexporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/modules/core/testutils/clocktest"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
)

// compareJSONWithTimestampHandling compares expected and actual JSON while handling timestamp fields properly
// to avoid flaky tests due to timing differences.
func compareJSONWithTimestampHandling(t *testing.T, expectedFilePath string, actualJSON []byte) {
	t.Helper()

	expected, err := os.ReadFile(expectedFilePath)
	require.NoError(t, err, "Failed to read expected JSON file")

	var expectedFlags map[string]any
	err = json.Unmarshal(expected, &expectedFlags)
	require.NoError(t, err, "Failed to unmarshal expected JSON")

	var actualFlags map[string]any
	err = json.Unmarshal(actualJSON, &actualFlags)
	require.NoError(t, err, "Failed to unmarshal actual JSON")

	// Handle cases where there might not be a "flags" field (e.g., offline, module_not_init)
	expectedFlagData, hasExpectedFlags := expectedFlags["flags"].(map[string]any)
	actualFlagData, hasActualFlags := actualFlags["flags"].(map[string]any)

	// Only proceed with timestamp validation if both have flags
	if hasExpectedFlags && hasActualFlags {
		// Compare structure without timestamps first
		for flagName, expectedFlag := range expectedFlagData {
			actualFlag, exists := actualFlagData[flagName]
			require.True(t, exists, "Flag %s should exist in actual results", flagName)

			expectedFlagObj, ok := expectedFlag.(map[string]any)
			require.True(t, ok, "expected flag %s should be a map", flagName)

			actualFlagObj, ok := actualFlag.(map[string]any)
			require.True(t, ok, "actual flag %s should be a map", flagName)

			// Verify timestamp exists and is reasonable
			require.Contains(t, actualFlagObj, "timestamp", "timestamp should exist in flag %s", flagName)
			actualTimestamp, ok := actualFlagObj["timestamp"].(float64)
			require.True(t, ok, "timestamp should be a number for flag %s", flagName)
			require.NotEqual(t, 0, actualTimestamp, "timestamp should not be zero for flag %s", flagName)

			// Timestamp should be recent (within a small delta of the current time)
			assert.InDelta(
				t,
				float64(clocktest.Instant().Unix()),
				actualTimestamp,
				5.0,
				"timestamp should be recent for flag %s",
				flagName,
			)

			// Normalize timestamps to compare structure
			expectedFlagObj["timestamp"] = actualFlagObj["timestamp"]
		}
	}

	// Now compare the full JSON structures
	expectedJSON, err := json.Marshal(expectedFlags)
	require.NoError(t, err, "Failed to marshal normalized expected JSON")
	assert.JSONEq(
		t,
		string(expectedJSON),
		string(actualJSON),
		"JSON structures should match after timestamp normalization",
	)
}

func TestAllFlagsState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		wantJSONFile string
		config       ffclient.Config
		wantValid    bool
		initModule   bool
	}{
		{
			name: "Valid multiple types",
			config: ffclient.Config{
				Retriever: &fileretriever.Retriever{
					Path: "./testdata/ffclient/all_flags/config_flag/flag-config-all-flags.yaml",
				},
				LeveledLogger: slog.Default(),
			},
			wantValid:    true,
			wantJSONFile: "./testdata/ffclient/all_flags/marshal_json/valid_multiple_types.json",
			initModule:   true,
		},
		{
			name: "module not init",
			config: ffclient.Config{
				Retriever: &fileretriever.Retriever{
					Path: "./testdata/ffclient/all_flags/config_flag/flag-config-all-flags.yaml",
				},
			},
			wantValid:    false,
			wantJSONFile: "./testdata/ffclient/all_flags/marshal_json/module_not_init.json",
			initModule:   false,
		},
		{
			name: "offline",
			config: ffclient.Config{
				Offline: true,
				Retriever: &fileretriever.Retriever{
					Path: "./testdata/ffclient/all_flags/config_flag/flag-config-all-flags.yaml",
				},
			},
			wantValid:    true,
			wantJSONFile: "./testdata/ffclient/all_flags/marshal_json/offline.json",
			initModule:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exportDir := t.TempDir()
			tt.config.DataExporter = ffclient.DataExporter{
				FlushInterval:    1000,
				MaxEventInMemory: 1,
				Exporter:         &fileexporter.Exporter{OutputDir: exportDir},
			}

			var goff *ffclient.GoFeatureFlag
			var err error
			if tt.initModule {
				goff, err = ffclient.New(tt.config)
				require.NoError(t, err)
				defer goff.Close()
			} else {
				// we close directly so we can test with module not init
				goff, _ = ffclient.New(tt.config)
				goff.Close()
			}

			user := ffcontext.NewEvaluationContext("random-key")
			allFlagsState := goff.AllFlagsState(user)
			assert.Equal(t, tt.wantValid, allFlagsState.IsValid())

			// Compare JSON output with proper timestamp handling
			marshaled, err := allFlagsState.MarshalJSON()
			require.NoError(t, err)
			compareJSONWithTimestampHandling(t, tt.wantJSONFile, marshaled)

			// no data exported
			files, _ := os.ReadDir(exportDir)
			assert.Empty(t, files)
		})
	}
}

func TestGetFlagStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		evaluationContext ffcontext.EvaluationContext
		name              string
		wantJSONFile      string
		config            ffclient.Config
		wantValid         bool
		initModule        bool
	}{
		{
			name: "Valid multiple flags",
			config: ffclient.Config{
				Retriever: &fileretriever.Retriever{
					Path: "./testdata/ffclient/get_flagstates/config_flag/flag-config-all-flags.yaml",
				},
			},
			wantValid:    true,
			wantJSONFile: "./testdata/ffclient/get_flagstates/marshal_json/valid_flag1_flag4.json",
			initModule:   true,
			evaluationContext: ffcontext.NewEvaluationContextBuilder("123").
				AddCustom("gofeatureflag", map[string]any{
					"flagList": []string{"test-flag1", "test-flag4"},
				}).
				Build(),
		},
		{
			name: "empty list of flags in context",
			config: ffclient.Config{
				Retriever: &fileretriever.Retriever{
					Path: "./testdata/ffclient/get_flagstates/config_flag/flag-config-all-flags.yaml",
				},
			},
			wantValid:    true,
			wantJSONFile: "./testdata/ffclient/get_flagstates/marshal_json/all_flags.json",
			initModule:   true,
			evaluationContext: ffcontext.NewEvaluationContextBuilder("123").
				AddCustom("gofeatureflag", map[string]any{
					"flagList": []string{},
				}).
				Build(),
		},
		{
			name: "no field in the context",
			config: ffclient.Config{
				Retriever: &fileretriever.Retriever{
					Path: "./testdata/ffclient/get_flagstates/config_flag/flag-config-all-flags.yaml",
				},
			},
			wantValid:         true,
			wantJSONFile:      "./testdata/ffclient/get_flagstates/marshal_json/all_flags.json",
			initModule:        true,
			evaluationContext: ffcontext.NewEvaluationContextBuilder("123").Build(),
		},
		{
			name: "offline",
			config: ffclient.Config{
				Offline: true,
				Retriever: &fileretriever.Retriever{
					Path: "./testdata/ffclient/all_flags/config_flag/flag-config-all-flags.yaml",
				},
			},
			wantValid:    true,
			wantJSONFile: "./testdata/ffclient/all_flags/marshal_json/offline.json",
			initModule:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// init logger
			exportDir := t.TempDir()
			tt.config.DataExporter = ffclient.DataExporter{
				FlushInterval:    1000,
				MaxEventInMemory: 1,
				Exporter:         &fileexporter.Exporter{OutputDir: exportDir},
			}

			var goff *ffclient.GoFeatureFlag
			var err error
			if tt.initModule {
				goff, err = ffclient.New(tt.config)
				require.NoError(t, err)
				defer goff.Close()
			} else {
				// we close directly so we can test with module not init
				goff, _ = ffclient.New(tt.config)
				goff.Close()
			}

			allFlagsState := goff.GetFlagStates(
				tt.evaluationContext,
				tt.evaluationContext.ExtractGOFFProtectedFields().FlagList,
			)
			assert.Equal(t, tt.wantValid, allFlagsState.IsValid())

			// Compare JSON output with proper timestamp handling
			marshaled, err := allFlagsState.MarshalJSON()
			require.NoError(t, err)
			compareJSONWithTimestampHandling(t, tt.wantJSONFile, marshaled)

			// no data exported
			files, _ := os.ReadDir(exportDir)
			assert.Empty(t, files)
		})
	}
}

func TestAllFlagsFromCache(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr    assert.ErrorAssertionFunc
		name       string
		config     ffclient.Config
		wantFlags  int
		initModule bool
	}{
		{
			name: "Valid multiple types",
			config: ffclient.Config{
				Retriever: &fileretriever.Retriever{
					Path: "./testdata/ffclient/all_flags/config_flag/flag-config-all-flags.yaml",
				},
			},
			initModule: true,
			wantFlags:  7,
			wantErr:    assert.NoError,
		},
		{
			name: "module not init",
			config: ffclient.Config{
				Retriever: &fileretriever.Retriever{
					Path: "./testdata/ffclient/all_flags/config_flag/flag-config-all-flags.yaml",
				},
			},
			initModule: false,
			wantErr:    assert.NoError,
		},
		{
			name: "offline",
			config: ffclient.Config{
				Offline: true,
				Retriever: &fileretriever.Retriever{
					Path: "./testdata/ffclient/all_flags/config_flag/flag-config-all-flags.yaml",
				},
			},
			initModule: true,
			wantErr:    assert.Error,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var goff *ffclient.GoFeatureFlag
			var err error
			if tt.initModule {
				goff, err = ffclient.New(tt.config)
				require.NoError(t, err)
				defer goff.Close()

				flags, getFlagsFromCacheErr := goff.GetFlagsFromCache()
				tt.wantErr(t, getFlagsFromCacheErr)

				assert.Len(t, flags, tt.wantFlags)
			} else {
				// we close directly so we can test with module not init
				goff, _ = ffclient.New(tt.config)
				goff.Close()

				_, getFlagsFromCacheErr := goff.GetFlagsFromCache()
				assert.Error(t, getFlagsFromCacheErr)
			}
		})
	}
}

// TestGetFlagsFromCacheReadOnly checks that the read-only snapshot serves the flags of the evaluations (the scheduled
// rollouts keep their memoized state between two calls) and that the public snapshot is detached from them.
func TestGetFlagsFromCacheReadOnly(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "flags.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`scheduled:
  variations: {A: a, B: b}
  defaultRule: {variation: A}
  scheduledRollout:
    - date: 2999-01-01T00:00:00Z
      defaultRule: {variation: B}
`), 0o600))
	goff, err := ffclient.New(ffclient.Config{Retriever: &fileretriever.Retriever{Path: path}})
	require.NoError(t, err)
	defer goff.Close()

	steps := func(flags map[string]flag.Flag) *[]flag.ScheduledStep {
		f, ok := flags["scheduled"].(*flag.InternalFlag)
		require.True(t, ok)
		require.NotNil(t, f.Scheduled)

		return f.Scheduled
	}
	readOnly1, err := goff.GetFlagsFromCacheReadOnly()
	require.NoError(t, err)
	readOnly2, err := goff.GetFlagsFromCacheReadOnly()
	require.NoError(t, err)
	copy1, err := goff.GetFlagsFromCache()
	require.NoError(t, err)
	copy2, err := goff.GetFlagsFromCache()
	require.NoError(t, err)

	assert.Same(t, steps(readOnly1), steps(readOnly2), "the read-only snapshots share the steps the memo is keyed on")
	assert.NotSame(t, steps(readOnly1), steps(copy1), "the public snapshot is detached from the live flags")
	assert.NotSame(t, steps(copy1), steps(copy2), "every public snapshot is its own copy")
}
