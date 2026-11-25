package ffclient_test

import (
	"bytes"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/modules/core/model"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
	"github.com/blackbirdworks/semaphore/testutils/mock"
	"github.com/blackbirdworks/semaphore/testutils/mock/mockretriever"
)

const (
	variationDefault       = "default"
	fooFlag                = "foo-flag"
	json2                  = "json"
	myFlag                 = "my-flag"
	randomKey              = "random-key"
	sDKdefault             = "SDKdefault"
	testFlag               = "test-flag"
	testdataFlagConfig2    = "testdata/flag-config-2nd-file.yaml"
	testdataFlagConfigYaml = "testdata/flag-config.yaml"
	unknownFlag            = "unknown-flag"
	userShouldHaveTest     = "User should have test flag"
	userShouldUseDefault   = "User should use default value if flag does not exists"
)

func TestStartWithoutRetriever(t *testing.T) {
	t.Parallel()

	got, err := ffclient.New(ffclient.Config{
		PollingInterval: 60 * time.Second,
		LeveledLogger:   slog.Default(),
	})
	assert.Nil(t, got)
	assert.EqualError(
		t,
		err,
		"impossible to initialize the retrievers, please check your configuration: no retriever in the configuration, i"+
			"mpossible to get the flags",
	)
}

func TestMultipleRetrievers(t *testing.T) {
	t.Parallel()

	client, err := ffclient.New(ffclient.Config{
		PollingInterval: 60 * time.Second,
		LeveledLogger:   slog.Default(),
		Retrievers: []retriever.Retriever{
			&fileretriever.Retriever{Path: testdataFlagConfig2},
			&fileretriever.Retriever{Path: testdataFlagConfigYaml},
		},
	})
	require.NoError(t, err)

	defer client.Close()

	user := ffcontext.NewEvaluationContext(randomKey)
	flagRes1, err := client.BoolVariationDetails(fooFlag, user, false)
	require.NoError(t, err)
	assert.True(t, flagRes1.Value)
	assert.NotEqual(t, flag.ErrorCodeFlagNotFound, flagRes1.ErrorCode)

	flagRes2, err := client.BoolVariationDetails(testFlag, user, false)
	require.NoError(t, err)
	assert.True(t, flagRes2.Value)
	assert.NotEqual(t, flag.ErrorCodeFlagNotFound, flagRes2.ErrorCode)
}
func TestMultipleRetrieversWithOverrideFlag(t *testing.T) {
	t.Parallel()

	client, err := ffclient.New(ffclient.Config{
		PollingInterval: 60 * time.Second,
		LeveledLogger:   slog.Default(),
		Retriever:       &fileretriever.Retriever{Path: "testdata/multiple_files/config-1.yaml"},
		Retrievers: []retriever.Retriever{
			&fileretriever.Retriever{Path: "testdata/multiple_files/config-2.yaml"},
		},
	})
	require.NoError(t, err)

	defer client.Close()

	user := ffcontext.NewEvaluationContext(randomKey)
	flagRes1, err := client.BoolVariationDetails(myFlag, user, false)
	require.NoError(t, err)
	assert.False(t, flagRes1.Value)
	assert.NotEqual(t, flag.ErrorCodeFlagNotFound, flagRes1.ErrorCode)

	flagRes2, err := client.BoolVariationDetails("my-3rd-flag", user, false)
	require.NoError(t, err)
	assert.True(t, flagRes2.Value)
	assert.NotEqual(t, flag.ErrorCodeFlagNotFound, flagRes2.ErrorCode)
}

func TestValidUseCase(t *testing.T) {
	t.Parallel()

	cliExport := mock.Exporter{Bulk: false}
	// Valid use case
	client, err := ffclient.New(ffclient.Config{
		PollingInterval: 5 * time.Second,
		Retriever:       &fileretriever.Retriever{Path: testdataFlagConfigYaml},
		LeveledLogger:   slog.Default(),
		DataExporters: []ffclient.DataExporter{
			{
				FlushInterval:    10 * time.Second,
				MaxEventInMemory: 1000,
				Exporter: &mock.Exporter{
					Bulk: true,
				},
			},
			{
				Exporter:          &cliExport,
				ExporterEventType: ffclient.TrackingEventExporter,
			},
		},
	})
	require.NoError(t, err)

	defer client.Close()

	user := ffcontext.NewEvaluationContext(randomKey)
	hasTestFlag, _ := client.BoolVariation(testFlag, user, false)
	assert.True(t, hasTestFlag, userShouldHaveTest)

	hasUnknownFlag, _ := client.BoolVariation(unknownFlag, user, false)
	assert.False(t, hasUnknownFlag, userShouldUseDefault)
	assert.NotEqual(t, time.Time{}, client.GetCacheRefreshDate())

	allFlags := client.AllFlagsState(user)
	assert.Len(t, allFlags.GetFlags(), 2)

	client.SetOffline(true)
	assert.True(t, client.IsOffline())
	assert.False(t, client.ForceRefresh())
	client.SetOffline(false)
	assert.False(t, client.IsOffline())
	assert.True(t, client.ForceRefresh())
	client.Track("toto", user, map[string]any{"key": value})
	assert.Len(t, cliExport.ExportedEvents, 1)
}

func TestValidUseCaseToml(t *testing.T) {
	t.Parallel()

	// Valid use case
	gffClient, err := ffclient.New(ffclient.Config{
		PollingInterval: 5 * time.Second,
		Retriever:       &fileretriever.Retriever{Path: "testdata/flag-config.toml"},
		LeveledLogger:   slog.Default(),
		FileFormat:      "toml",
	})
	require.NoError(t, err)

	defer gffClient.Close()

	user := ffcontext.NewEvaluationContext(randomKey)
	hasTestFlag, _ := gffClient.BoolVariation(testFlag, user, false)
	assert.True(t, hasTestFlag, userShouldHaveTest)

	hasUnknownFlag, _ := gffClient.BoolVariation(unknownFlag, user, false)
	assert.False(t, hasUnknownFlag, userShouldUseDefault)
}

func TestValidUseCaseJson(t *testing.T) {
	t.Parallel()

	// Valid use case
	gffClient, err := ffclient.New(ffclient.Config{
		PollingInterval: 5 * time.Second,
		Retriever:       &fileretriever.Retriever{Path: "testdata/flag-config.json"},
		LeveledLogger:   slog.Default(),
		FileFormat:      json2,
	})
	require.NoError(t, err)

	defer gffClient.Close()

	user := ffcontext.NewEvaluationContext(randomKey)
	hasTestFlag, _ := gffClient.BoolVariation(testFlag, user, false)
	assert.True(t, hasTestFlag, userShouldHaveTest)

	hasUnknownFlag, _ := gffClient.BoolVariation(unknownFlag, user, false)
	assert.False(t, hasUnknownFlag, userShouldUseDefault)
	assert.NotEqual(t, time.Time{}, gffClient.GetCacheRefreshDate())
}

func TestValidUseCaseMultilineQueryJson(t *testing.T) {
	t.Parallel()

	// Valid use case
	gffClient, err := ffclient.New(ffclient.Config{
		PollingInterval: 5 * time.Second,
		Retriever: &fileretriever.Retriever{
			Path: "testdata/flag-config-multiline-query.json",
		},
		LeveledLogger: slog.Default(),
		FileFormat:    json2,
	})
	require.NoError(t, err)

	defer gffClient.Close()

	user := ffcontext.NewEvaluationContext(randomKey)
	hasTestFlag, _ := gffClient.BoolVariation(testFlag, user, false)
	assert.True(t, hasTestFlag, userShouldHaveTest)

	hasUnknownFlag, _ := gffClient.BoolVariation(unknownFlag, user, false)
	assert.False(t, hasUnknownFlag, userShouldUseDefault)
	assert.NotEqual(t, time.Time{}, gffClient.GetCacheRefreshDate())
}

func Test2GoFeatureFlagInstance(t *testing.T) {
	t.Parallel()

	gffClient1, err := ffclient.New(ffclient.Config{
		PollingInterval: 5 * time.Second,
		Retriever:       &fileretriever.Retriever{Path: testdataFlagConfigYaml},
		LeveledLogger:   slog.Default(),
	})
	require.NoError(t, err)

	defer gffClient1.Close()

	gffClient2, err2 := ffclient.New(ffclient.Config{
		PollingInterval: 10 * time.Second,
		Retriever:       &fileretriever.Retriever{Path: "testdata/test-instance2.yaml"},
		LeveledLogger:   slog.Default(),
	})
	require.NoError(t, err2)

	defer gffClient2.Close()

	user := ffcontext.NewEvaluationContext(randomKey)

	// Client1 is supposed to have the flag at true
	hasTestFlagClient1, _ := gffClient1.BoolVariation(testFlag, user, false)
	assert.True(t, hasTestFlagClient1, userShouldHaveTest)

	// Client2 is supposed to have the flag at true
	hasTestFlagClient2, _ := gffClient2.BoolVariation(testFlag, user, false)
	assert.False(t, hasTestFlagClient2, userShouldHaveTest)
}

func TestUpdateFlag(t *testing.T) {
	t.Parallel()

	initialFileContent := `
test-flag:
  variations:
    true_var: true
    false_var: false
  targeting:
    - query: key eq "random-key"
      percentage:
        true_var: 100
        false_var: 0
  defaultRule:
    variation: false_var`

	flagFile, _ := os.CreateTemp(t.TempDir(), "")
	_ = os.WriteFile(flagFile.Name(), []byte(initialFileContent), os.ModePerm)

	gffClient1, _ := ffclient.New(ffclient.Config{
		PollingInterval: 1 * time.Second,
		Retriever:       &fileretriever.Retriever{Path: flagFile.Name()},
		LeveledLogger:   slog.Default(),
	})
	defer gffClient1.Close()

	flagValue, _ := gffClient1.BoolVariation(
		testFlag,
		ffcontext.NewEvaluationContext(randomKey),
		false,
	)
	assert.True(t, flagValue)

	updatedFileContent := `
test-flag:
  variations:
    true_var: true
    false_var: false
  targeting:
    - query: key eq "random-key2"
      percentage:
        true_var: 100
        false_var: 0
  defaultRule:
    variation: false_var`

	_ = os.WriteFile(flagFile.Name(), []byte(updatedFileContent), os.ModePerm)

	flagValue, _ = gffClient1.BoolVariation(
		testFlag,
		ffcontext.NewEvaluationContext(randomKey),
		false,
	)
	assert.True(t, flagValue)

	// the poller (every second) picks the new content up
	require.Eventually(t, func() bool {
		got, evalErr := gffClient1.BoolVariation(
			testFlag,
			ffcontext.NewEvaluationContext(randomKey),
			false,
		)

		return evalErr == nil && !got
	}, 30*time.Second, 20*time.Millisecond, "the updated file was never picked up")
}

func TestImpossibleToLoadfile(t *testing.T) {
	t.Parallel()

	initialFileContent := `
test-flag:
  variations:
    true_var: true
    false_var: false
  targeting:
    - query: key eq "random-key"
      percentage:
        true_var: 100
        false_var: 0
  defaultRule:
    variation: false_var`

	flagFile, _ := os.CreateTemp(t.TempDir(), "impossible")
	_ = os.WriteFile(flagFile.Name(), []byte(initialFileContent), os.ModePerm)

	gffClient1, _ := ffclient.New(ffclient.Config{
		PollingInterval: 1 * time.Second,
		Retriever:       &fileretriever.Retriever{Path: flagFile.Name()},
		LeveledLogger:   slog.Default(),
	})
	defer gffClient1.Close()

	flagValue, _ := gffClient1.BoolVariation(
		testFlag,
		ffcontext.NewEvaluationContext(randomKey),
		false,
	)
	assert.True(t, flagValue)

	flagValue, _ = gffClient1.BoolVariation(
		testFlag,
		ffcontext.NewEvaluationContext(randomKey),
		false,
	)
	assert.True(t, flagValue)

	// remove file we should still take the last version in consideration
	os.Remove(flagFile.Name())
	time.Sleep(2 * time.Second)

	flagValue, _ = gffClient1.BoolVariation(
		testFlag,
		ffcontext.NewEvaluationContext(randomKey),
		false,
	)
	assert.True(t, flagValue)
}

func TestFlagFileUnreachable(t *testing.T) {
	t.Parallel()

	initialFileContent := `
test-flag:
  variations:
    true_var: "true"
    false_var: "false"
  targeting:
    - query: key eq "random-key"
      percentage:
        true_var: 100
        false_var: 0
  defaultRule:
    variation: false_var`

	tempDir := t.TempDir()

	flagFilePath := tempDir + "_FlagFileUnreachable.yaml"
	gff, err := ffclient.New(ffclient.Config{
		PollingInterval:         1 * time.Second,
		Retriever:               &fileretriever.Retriever{Path: flagFilePath},
		LeveledLogger:           slog.Default(),
		StartWithRetrieverError: true,
	})
	require.NoError(t, err, "should not return any error even if we can't retrieve the file")

	defer gff.Close()

	flagValue, _ := gff.StringVariation(
		testFlag,
		ffcontext.NewEvaluationContext(randomKey),
		sDKdefault,
	)
	assert.Equal(t, sDKdefault, flagValue, "should use the SDK default value")

	err = os.WriteFile(flagFilePath, []byte(initialFileContent), os.ModePerm)
	require.NoError(t, err)

	// the poller picks the file up once it exists
	require.Eventually(t, func() bool {
		flagValue, _ = gff.StringVariation(
			testFlag,
			ffcontext.NewEvaluationContext(randomKey),
			sDKdefault,
		)

		return flagValue == "true"
	}, 30*time.Second, 20*time.Millisecond, "the file was never picked up")
	assert.Equal(t, "true", flagValue, "should use the true value")
}

func TestInvalidConf(t *testing.T) {
	t.Parallel()

	gff, err := ffclient.New(ffclient.Config{
		PollingInterval: 1 * time.Second,
		Retriever:       &fileretriever.Retriever{Path: "testdata/invalid-flag-config.json"},
		LeveledLogger:   slog.Default(),
	})
	require.Error(t, err)

	defer gff.Close()

	assert.Equal(
		t,
		"impossible to initialize the retrievers, please check your configuration: impossible to retrieve the flags, pl"+
			"ease check your configuration: decoding the flags: yaml: line 43: did not find expected ',' or '}'",
		err.Error(),
	)
}

func TestInvalidConfAndRetrieverError(t *testing.T) {
	t.Parallel()

	gff, err := ffclient.New(ffclient.Config{
		PollingInterval: 1 * time.Second,
		Retriever: &fileretriever.Retriever{
			Path: "testdata/invalid-flag-config.json",
		},
		LeveledLogger:           slog.Default(),
		StartWithRetrieverError: true,
	})
	require.NoError(t, err)

	defer gff.Close()
	// nothing could be loaded: the flag is unknown and the default value is returned
	got, err := gff.BoolVariationDetails(testFlag, ffcontext.NewEvaluationContext(randomKey), false)
	require.EqualError(t, err, "flag test-flag is not present or disabled")
	assert.Equal(t, model.VariationResult[bool]{
		TrackEvents:   true,
		VariationType: "SdkDefault",
		Failed:        true,
		Reason:        flag.ReasonError,
		ErrorCode:     flag.ErrorCodeFlagNotFound,
		Value:         false,
	}, got)
}

func TestValidUseCaseBigFlagFile(t *testing.T) {
	t.Parallel()

	// Valid use case
	gff, err := ffclient.New(ffclient.Config{
		PollingInterval: 5 * time.Second,
		Retriever:       &fileretriever.Retriever{Path: "testdata/flag-config-big.yaml"},
	})
	require.NoError(t, err)

	defer gff.Close()

	user := ffcontext.NewEvaluationContext(randomKey)
	hasTestFlag, _ := gff.BoolVariation("test-flag99", user, false)
	assert.True(t, hasTestFlag, userShouldHaveTest)

	hasUnknownFlag, _ := gff.BoolVariation(unknownFlag, user, false)
	assert.False(t, hasUnknownFlag, userShouldUseDefault)
}

func TestInitializableRetrieverWithRetrieverReady(t *testing.T) {
	t.Parallel()

	tmpFile, err := os.CreateTemp(t.TempDir(), "")
	assert.NoError(t, err)
	// close the handle before removing: Windows cannot delete a file that is still open
	assert.NoError(t, tmpFile.Close())
	// we delete the fileTemp to be sure that the retriever will have to create the file
	err = os.Remove(tmpFile.Name())
	require.NoError(t, err)

	r := mockretriever.NewFileInitializableRetriever(tmpFile.Name(), retriever.RetrieverReady)
	gff, err := ffclient.New(ffclient.Config{
		PollingInterval: 5 * time.Second,
		Retriever:       r,
	})
	require.NoError(t, err)

	user := ffcontext.NewEvaluationContext(randomKey)
	hasTestFlag, _ := gff.BoolVariation("flag-xxxx-123", user, false)
	assert.True(t, hasTestFlag, userShouldHaveTest)

	gff.Close()

	_, err = os.Stat(tmpFile.Name())
	assert.ErrorIs(t, err, os.ErrNotExist)
}
func TestInitializableRetrieverWithRetrieverNotReady(t *testing.T) {
	t.Parallel()

	tmpFile, err := os.CreateTemp(t.TempDir(), "")
	assert.NoError(t, err)
	// close the handle before removing: Windows cannot delete a file that is still open
	assert.NoError(t, tmpFile.Close())
	// we delete the fileTemp to be sure that the retriever will have to create the file
	err = os.Remove(tmpFile.Name())
	require.NoError(t, err)

	r := mockretriever.NewFileInitializableRetriever(tmpFile.Name(), retriever.RetrieverNotReady)
	gff, err := ffclient.New(ffclient.Config{
		PollingInterval: 5 * time.Second,
		Retriever:       r,
	})
	require.NoError(t, err)

	defer gff.Close()

	user := ffcontext.NewEvaluationContext(randomKey)
	hasTestFlag, _ := gff.BoolVariation("flag-xxxx-123", user, false)
	assert.False(t, hasTestFlag, "Should resolve to default value if retriever is not ready")
}

func TestGoFeatureFlag_GetCacheRefreshDate(t *testing.T) {
	t.Parallel()

	type fields struct {
		pollingInterval time.Duration
		waitingDuration time.Duration
	}

	tests := []struct {
		name          string
		fields        fields
		wantRefreshed bool
		offline       bool
	}{
		{
			name:          "Should be refreshed",
			fields:        fields{waitingDuration: 5 * time.Second, pollingInterval: 1 * time.Second},
			wantRefreshed: true,
		},
		{
			// The polling interval is far longer than the waiting duration, so a single tick
			// can never happen during the wait, even if time.Sleep overshoots (which it does
			// more on Windows, where the timer granularity is ~15ms).
			name:          "Should not be refreshed",
			fields:        fields{waitingDuration: 500 * time.Millisecond, pollingInterval: 30 * time.Second},
			wantRefreshed: false,
		},
		{
			name:          "Should not crash in offline mode",
			fields:        fields{waitingDuration: 500 * time.Millisecond, pollingInterval: 30 * time.Second},
			wantRefreshed: false,
			offline:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gff, err := ffclient.New(ffclient.Config{
				PollingInterval: tt.fields.pollingInterval,
				Retriever:       &fileretriever.Retriever{Path: testdataFlagConfigYaml},
				Offline:         tt.offline,
			})
			require.NoError(t, err)

			defer gff.Close()

			date1 := gff.GetCacheRefreshDate()

			if tt.wantRefreshed {
				// Waiting exactly one polling interval leaves no margin for the scheduler,
				// so we poll for the refresh instead of sleeping for a fixed duration.
				require.Eventually(t, func() bool { return date1.Before(gff.GetCacheRefreshDate()) },
					tt.fields.waitingDuration, 50*time.Millisecond,
					"the cache was never refreshed by the background updater")
			} else {
				require.Never(t, func() bool { return date1.Before(gff.GetCacheRefreshDate()) },
					tt.fields.waitingDuration, 50*time.Millisecond,
					"the cache was refreshed even though the polling interval had not elapsed")
			}

			date2 := gff.GetCacheRefreshDate()

			if !tt.offline {
				assert.NotEqual(t, time.Time{}, date1)
				assert.NotEqual(t, time.Time{}, date2)
			}

			assert.Equal(t, tt.wantRefreshed, date1.Before(date2))
		})
	}
}

func TestGoFeatureFlag_SetOffline(t *testing.T) {
	t.Parallel()

	gffClient, err := ffclient.New(ffclient.Config{
		PollingInterval: 1 * time.Second,
		Retriever:       &fileretriever.Retriever{Path: testdataFlagConfigYaml},
		LeveledLogger:   slog.Default(),
		Offline:         false,
	})
	require.NoError(t, err)

	defer gffClient.Close()

	gffClient.SetOffline(true)
	assert.True(t, gffClient.IsOffline())

	time.Sleep(2 * time.Second)

	gffClient.SetOffline(false)
	assert.False(t, gffClient.IsOffline())
}

func Test_GetPollingInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		pollingInterval time.Duration
		wantMillis      int64
	}{
		{
			name:            "60 seconds",
			pollingInterval: 60 * time.Second,
			wantMillis:      60_000,
		},
		{
			name:            "6 hour",
			pollingInterval: 6 * time.Hour,
			wantMillis:      21_600_000,
		},
		{
			name:            "an interval below the minimum is raised to one second",
			pollingInterval: 2,
			wantMillis:      1000,
		},
		{
			name:            "no interval uses the default of 60 seconds",
			pollingInterval: 0,
			wantMillis:      60_000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			goff, err := ffclient.New(ffclient.Config{
				PollingInterval: tt.pollingInterval,
				Retriever:       &fileretriever.Retriever{Path: testdataFlagConfigYaml},
			})
			require.NoError(t, err)
			assert.Equal(t, tt.wantMillis, goff.GetPollingInterval())
		})
	}
}

func Test_ForceRefreshCache(t *testing.T) {
	t.Parallel()

	tempFile, err := os.CreateTemp(t.TempDir(), "")
	assert.NoError(t, err)
	// close the handle before reusing/removing it: Windows cannot write to or
	// delete a file that is still open by another handle
	assert.NoError(t, tempFile.Close())
	defer func() { _ = os.Remove(tempFile.Name()) }()

	content, err := os.ReadFile(testdataFlagConfigYaml)
	require.NoError(t, err)
	err = os.WriteFile(tempFile.Name(), content, os.ModePerm)
	require.NoError(t, err)

	gffClient, err := ffclient.New(ffclient.Config{
		PollingInterval: 15 * time.Minute,
		Retriever:       &fileretriever.Retriever{Path: tempFile.Name()},
		LeveledLogger:   slog.Default(),
		Offline:         false,
	})
	require.NoError(t, err)

	defer gffClient.Close()

	refreshTime := gffClient.GetCacheRefreshDate()

	// modify the file to trigger a refresh
	newContent, err := os.ReadFile(testdataFlagConfig2)
	require.NoError(t, err)
	err = os.WriteFile(tempFile.Name(), newContent, os.ModePerm)
	require.NoError(t, err)
	// checking that the cache has not been refreshed
	assert.Equal(t, refreshTime, gffClient.GetCacheRefreshDate())

	// the stepping clock of TestMain makes the forced refresh land on a strictly later instant
	// checking that the cache has been refreshed
	gffClient.ForceRefresh()
	assert.NotEqual(t, refreshTime, gffClient.GetCacheRefreshDate())
	gffClient.SetOffline(true)
	gffClient.ForceRefresh()
	assert.Equal(t, time.Time{}, gffClient.GetCacheRefreshDate())
}

// readPersistedFlags returns the content of the persistent flag configuration file and the
// flags it contains. The library writes this file from a goroutine, so a read can catch a
// partial write: in that case ok is false and the caller (a require.Eventually) just retries.
func readPersistedFlags(path string) ([]byte, map[string]any, bool) {
	var flags map[string]any

	content, err := os.ReadFile(path)
	if err != nil || len(content) == 0 {
		return nil, nil, false
	}

	unmarshalErr := yaml.Unmarshal(content, &flags)
	if unmarshalErr != nil || len(flags) == 0 {
		return nil, nil, false
	}

	return content, flags, true
}

func Test_PersistFlagConfigurationOnDisk(t *testing.T) {
	t.Parallel()

	// Note: Config.Initialize() raises any polling interval below 1s to 1s (see
	// adjustPollingInterval in config.go), so both clients below really poll once per second.
	// Waiting exactly one polling interval with time.Sleep leaves no margin for the OS
	// scheduler (the Windows timer granularity alone is ~15ms), which is why every "wait
	// until something happened" below is a require.Eventually.
	const (
		pollingInterval   = 1 * time.Second
		eventuallyTimeout = 10 * pollingInterval
		eventuallyTick    = 20 * time.Millisecond
	)

	configFile1, err := os.CreateTemp(t.TempDir(), "")
	require.NoError(t, err)
	// close the handles before reusing/removing them: Windows cannot write to or
	// delete a file that is still open by another handle
	require.NoError(t, configFile1.Close())

	persistFile, err := os.CreateTemp(t.TempDir(), "")
	require.NoError(t, err)
	require.NoError(t, persistFile.Close())
	// t.Cleanup runs after the deferred Close() calls below, so no handle is still open on
	// these files when Windows deletes them.
	t.Cleanup(func() {
		_ = os.Remove(configFile1.Name())
		_ = os.Remove(persistFile.Name())
	})

	content, err := os.ReadFile(testdataFlagConfigYaml)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configFile1.Name(), content, os.ModePerm))

	gffClient, err := ffclient.New(ffclient.Config{
		PollingInterval:                 pollingInterval,
		Retriever:                       &fileretriever.Retriever{Path: configFile1.Name()},
		LeveledLogger:                   slog.Default(),
		Offline:                         false,
		PersistentFlagConfigurationFile: persistFile.Name(),
	})
	require.NoError(t, err)

	// 1. Checking that the persistence happened (the file is written by a goroutine).
	var contentP []byte

	require.Eventually(t, func() bool {
		c, flags, ok := readPersistedFlags(persistFile.Name())
		if !ok || len(flags) != 2 { // testdata/flag-config.yaml contains 2 flags
			return false
		}

		contentP = c

		return true
	}, eventuallyTimeout, eventuallyTick,
		"the initial flag configuration was never persisted on disk")

	// 2. Modifying the configuration file
	content2, err := os.ReadFile(testdataFlagConfig2)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configFile1.Name(), content2, os.ModePerm))

	// 3. Checking that the persistence happened again and that the content is different.
	// We wait for a flag of the 2nd file instead of only comparing the raw bytes, so that a
	// partially written file can never satisfy the condition.
	var contentP2 []byte

	require.Eventually(t, func() bool {
		persistedBytes, flags, ok := readPersistedFlags(persistFile.Name())
		if !ok || len(flags) != 2 {
			return false
		}

		if _, hasFooFlag := flags[fooFlag]; !hasFooFlag {
			return false
		}

		contentP2 = persistedBytes

		return true
	}, eventuallyTimeout, eventuallyTick,
		"the updated flag configuration was never persisted on disk")
	assert.NotEqual(t, contentP, contentP2)

	// 4. Stopping Semaphore and restart with a retriever that will fail.
	// Close() stops the polling: without it the background updater of gffClient would keep
	// rewriting persistFile behind the back of gffClient2.
	gffClient.Close()

	configFile2, err := os.CreateTemp(t.TempDir(), "")
	require.NoError(t, err)
	// close the handle before removing: Windows cannot delete a file that is still open
	require.NoError(t, configFile2.Close())
	require.NoError(t, os.Remove(configFile2.Name()))
	t.Cleanup(func() { _ = os.Remove(configFile2.Name()) })

	gffClient2, err := ffclient.New(ffclient.Config{
		PollingInterval:                 pollingInterval,
		Retriever:                       &fileretriever.Retriever{Path: configFile2.Name()},
		LeveledLogger:                   slog.Default(),
		Offline:                         false,
		PersistentFlagConfigurationFile: persistFile.Name(),
	})
	require.NoError(t, err)

	defer gffClient2.Close()

	// 5. Checking that the flags have been loaded from the persistent file. The fallback is
	// done synchronously by ffclient.New (retrievePersistentLocalDisk), nothing to wait for.
	details, _ := gffClient2.BoolVariationDetails(
		fooFlag,
		ffcontext.NewEvaluationContext(randomKey),
		false,
	)
	assert.NotEqual(t, "ERROR", details.Reason)

	// 5b. The retriever keeps failing: checking that the flags coming from the persistent file
	// survive the failing polls. This is the only assertion that needs a fixed duration,
	// because we assert that something does NOT happen.
	require.Never(t, func() bool {
		flags, errCache := gffClient2.GetFlagsFromCache()

		return errCache != nil || len(flags) != 2
	}, 2*pollingInterval, eventuallyTick,
		"the flags loaded from the persistent file were lost on a failing poll")

	// 6. Making the configuration file of the failing retriever available
	content3, err := os.ReadFile("testdata/flag-config-3rd-file.yaml")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configFile2.Name(), content3, os.ModePerm))

	// 7. Checking that the polling goes on after a retriever error, and that the flags of the
	// 3rd file are now served (testdata/flag-config-3rd-file.yaml contains 7 flags).
	require.Eventually(t, func() bool {
		flags2, errCache := gffClient2.GetFlagsFromCache()

		return errCache == nil && len(flags2) == 7
	}, eventuallyTimeout, eventuallyTick,
		"the retriever did not recover after the configuration file was restored")

	// 8. Checking that the persistence happened and that the file is different from the
	// previous one.
	require.Eventually(t, func() bool {
		c, flags3, ok := readPersistedFlags(persistFile.Name())

		return ok && len(flags3) == 7 && !bytes.Equal(contentP2, c)
	}, eventuallyTimeout, eventuallyTick,
		"the new flag configuration was never persisted on disk")
}

func TestUseCustomBucketingKey(t *testing.T) {
	t.Parallel()

	gffClient, err := ffclient.New(ffclient.Config{
		PollingInterval: 1 * time.Second,
		Retriever: &fileretriever.Retriever{
			Path: "testdata/flag-config-custom-bucketingkey.yaml",
		},
		LeveledLogger: slog.Default(),
		Offline:       false,
	})
	require.NoError(t, err)

	t.Run("should return the default value if the bucketing key is not found", func(t *testing.T) {
		t.Parallel()

		got, stringVariationDetailsErr := gffClient.StringVariationDetails(
			myFlag,
			ffcontext.NewEvaluationContext(randomKey),
			variationDefault,
		)
		require.NoError(t, stringVariationDetailsErr)

		want := model.VariationResult[string]{
			Value:         variationDefault,
			TrackEvents:   true,
			VariationType: "SdkDefault",
			Failed:        true,
			Reason:        flag.ReasonError,
			ErrorCode:     flag.ErrorCodeTargetingKeyMissing,
			ErrorDetails:  "impossible to find bucketingKey in context: nested key not found: teamId",
		}
		assert.Equal(t, want, got)
	})

	t.Run("should return the variation value if the bucketing key is found", func(t *testing.T) {
		t.Parallel()

		got, stringVariationDetailsErr := gffClient.StringVariationDetails(
			myFlag,
			ffcontext.NewEvaluationContextBuilder(randomKey).
				AddCustom("teamId", "team-123").
				Build(),
			variationDefault,
		)
		require.NoError(t, stringVariationDetailsErr)

		want := model.VariationResult[string]{
			Value:         "value_A",
			TrackEvents:   true,
			VariationType: "variation_A",
			Failed:        false,
			Reason:        flag.ReasonStatic,
			Cacheable:     true,
		}
		assert.Equal(t, want, got)
	})
}

func Test_DisableNotifierOnInit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		config               *ffclient.Config
		name                 string
		disableNotification  bool
		expectedNotifyCalled bool
	}{
		{
			name: "DisableNotifierOnInit is true",
			config: &ffclient.Config{
				PollingInterval:       60 * time.Second,
				Retriever:             &fileretriever.Retriever{Path: testdataFlagConfigYaml},
				DisableNotifierOnInit: true,
			},
			expectedNotifyCalled: false,
		},
		{
			name: "DisableNotifierOnInit is false",
			config: &ffclient.Config{
				PollingInterval:       60 * time.Second,
				Retriever:             &fileretriever.Retriever{Path: testdataFlagConfigYaml},
				DisableNotifierOnInit: false,
			},
			expectedNotifyCalled: true,
		},
		{
			name: "DisableNotifierOnInit is not set",
			config: &ffclient.Config{
				PollingInterval: 60 * time.Second,
				Retriever:       &fileretriever.Retriever{Path: testdataFlagConfigYaml},
			},
			expectedNotifyCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockNotifier := &mock.Notifier{}
			tt.config.Notifiers = []notifier.Notifier{mockNotifier}

			gffClient, err := ffclient.New(*tt.config)
			require.NoError(t, err)

			defer gffClient.Close()

			time.Sleep(2 * time.Second) // wait for the goroutine to call Notify()
			assert.Equal(t, tt.expectedNotifyCalled, mockNotifier.GetNotifyCalls() > 0)
		})
	}
}

func TestStartWithNegativeIntervalToDisablePolling(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(testdataFlagConfigYaml)
	require.NoError(t, err)

	// copy of the file
	tempFile, err := os.CreateTemp(t.TempDir(), "")
	require.NoError(t, err)

	defer func() { _ = os.Remove(tempFile.Name()) }()

	err = os.WriteFile(tempFile.Name(), content, os.ModePerm)
	require.NoError(t, err)

	goff, err := ffclient.New(ffclient.Config{
		PollingInterval: -1 * time.Second,
		Retriever:       &fileretriever.Retriever{Path: tempFile.Name()},
		LeveledLogger:   slog.Default(),
	})
	require.NoError(t, err)

	cacheRefresh := goff.GetCacheRefreshDate()

	// modify the file to trigger a refresh
	newContent, err := os.ReadFile(testdataFlagConfig2)
	require.NoError(t, err)
	err = os.WriteFile(tempFile.Name(), newContent, os.ModePerm)
	require.NoError(t, err)

	// wait to be sure we give time to the goroutine to refresh the cache
	time.Sleep(2 * time.Second)

	assert.Equal(t, cacheRefresh, goff.GetCacheRefreshDate())

	// we force a refresh to check if the cache is refreshed
	goff.ForceRefresh()
	assert.NotEqual(t, cacheRefresh, goff.GetCacheRefreshDate())
}

func TestGoFeatureFlag_GetEvaluationContextEnrichment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		enrichment     map[string]any
		wantEnrichment map[string]any
		name           string
	}{
		{
			name:       "nil enrichment",
			enrichment: nil, wantEnrichment: nil,
		},
		{
			name:       "empty enrichment",
			enrichment: map[string]any{}, wantEnrichment: map[string]any{},
		},
		{
			name: "non-empty enrichment",
			enrichment: map[string]any{
				"foo": "bar",
				"num": 42,
			},
			wantEnrichment: map[string]any{"foo": "bar", "num": 42},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gff, err := ffclient.New(ffclient.Config{
				Retriever:                   &fileretriever.Retriever{Path: testdataFlagConfigYaml},
				EvaluationContextEnrichment: tt.enrichment,
			})
			require.NoError(t, err)

			got := gff.GetEvaluationContextEnrichment()
			assert.Equal(t, tt.wantEnrichment, got)
		})
	}
}

func TestGoFeatureFlag_SetOfflineWithContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		offline         bool
		wantOffline     bool
		wantForceResult bool
	}{
		{name: "going offline", offline: true, wantOffline: true, wantForceResult: false},
		{name: "staying online", offline: false, wantOffline: false, wantForceResult: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, err := ffclient.New(ffclient.Config{
				PollingInterval: 1 * time.Second,
				Retriever:       &fileretriever.Retriever{Path: testdataFlagConfigYaml},
			})
			require.NoError(t, err)

			defer client.Close()

			client.SetOfflineWithContext(t.Context(), tt.offline)

			assert.Equal(t, tt.wantOffline, client.IsOffline())
			assert.Equal(t, tt.wantForceResult, client.ForceRefresh(), "an offline client does not refresh")
		})
	}
}

func TestGoFeatureFlag_EvaluationService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		offline bool
		// wantShared is whether the two clients share their Service
		wantShared bool
	}{
		{name: "online client", offline: false, wantShared: false},
		{name: "offline client", offline: true, wantShared: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			first, err := ffclient.New(ffclient.Config{
				Offline:   tt.offline,
				Retriever: &fileretriever.Retriever{Path: testdataFlagConfigYaml},
			})
			require.NoError(t, err)

			defer first.Close()

			second, err := ffclient.New(ffclient.Config{
				Offline:   tt.offline,
				Retriever: &fileretriever.Retriever{Path: testdataFlagConfigYaml},
			})
			require.NoError(t, err)

			defer second.Close()

			require.NotNil(t, first.EvaluationService())
			assert.Equal(t, tt.wantShared, first.EvaluationService() == second.EvaluationService(),
				"two clients never share their Service")
		})
	}
}
