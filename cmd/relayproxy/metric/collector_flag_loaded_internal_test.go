package metric

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	semaphoreFlagLoaded = "semaphore_flag_loaded"
)

var (
	errNoFlagsetsConfigured = errors.New("no flagsets configured")
)

type fakeFlagsetSource struct {
	flagsets map[string]*ffclient.GoFeatureFlag
	err      error
}

func (f fakeFlagsetSource) AllFlagSets() (map[string]*ffclient.GoFeatureFlag, error) {
	return f.flagsets, f.err
}

func newFileClient(t *testing.T, content string) (*ffclient.GoFeatureFlag, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flags.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	client, err := ffclient.New(&ffclient.Config{
		PollingInterval: time.Hour,
		Retriever:       &fileretriever.Retriever{Path: path},
	})
	require.NoError(t, err)
	t.Cleanup(client.Close)

	return client, path
}

const flagsYAML = `
flag-on:
  variations:
    enabled: true
    disabled: false
  defaultRule:
    variation: enabled
flag-off:
  variations:
    enabled: true
    disabled: false
  defaultRule:
    variation: enabled
  disable: true
`

func TestFlagLoaded_ExposesOneSeriesPerFlagPerFlagset(t *testing.T) {
	t.Parallel()

	teamA, _ := newFileClient(t, flagsYAML)
	teamB, _ := newFileClient(t, `
other-flag:
  variations:
    enabled: true
  defaultRule:
    variation: enabled
`)
	metrics, err := NewMetrics()
	require.NoError(t, err)
	require.NoError(t, metrics.RegisterFlagLoaded(fakeFlagsetSource{
		flagsets: map[string]*ffclient.GoFeatureFlag{"team-a": teamA, "team-b": teamB},
	}))

	expected := `
# HELP semaphore_flag_loaded Flags currently loaded in the relay proxy cache, one series per flag with value 1.
# TYPE semaphore_flag_loaded gauge
semaphore_flag_loaded{disabled="false",flag_name="flag-on",flagset="team-a"} 1
semaphore_flag_loaded{disabled="true",flag_name="flag-off",flagset="team-a"} 1
semaphore_flag_loaded{disabled="false",flag_name="other-flag",flagset="team-b"} 1
`
	assert.NoError(t, testutil.GatherAndCompare(metrics.Registry, strings.NewReader(expected), semaphoreFlagLoaded))
}

func TestFlagLoaded_DisabledReflectsScheduledStepsAndExperimentation(t *testing.T) {
	t.Parallel()

	client, _ := newFileClient(t, `
scheduled-off:
  variations:
    enabled: true
  defaultRule:
    variation: enabled
  scheduledRollout:
    - date: 2020-01-01T00:00:00Z
      disable: true
scheduled-back-on:
  variations:
    enabled: true
  defaultRule:
    variation: enabled
  disable: true
  scheduledRollout:
    - date: 2020-01-01T00:00:00Z
      disable: false
future-off:
  variations:
    enabled: true
  defaultRule:
    variation: enabled
  scheduledRollout:
    - date: 2999-01-01T00:00:00Z
      disable: true
experiment-over:
  variations:
    enabled: true
  defaultRule:
    variation: enabled
  experimentation:
    start: 2020-01-01T00:00:00Z
    end: 2020-02-01T00:00:00Z
percentage-with-bucketing-key:
  bucketingKey: teamId
  variations:
    enabled: true
    disabled: false
  defaultRule:
    percentage:
      enabled: 50
      disabled: 50
disabled-with-nested-bucketing-key:
  bucketingKey: company.id
  variations:
    enabled: true
  defaultRule:
    variation: enabled
  disable: true
percentage-with-nested-bucketing-key:
  bucketingKey: company.id
  variations:
    enabled: true
    disabled: false
  defaultRule:
    percentage:
      enabled: 50
      disabled: 50
scheduled-off-with-bucketing-key:
  bucketingKey: teamId
  variations:
    enabled: true
  defaultRule:
    variation: enabled
  scheduledRollout:
    - date: 2020-01-01T00:00:00Z
      disable: true
experiment-over-with-nested-bucketing-key:
  bucketingKey: company.id
  variations:
    enabled: true
  defaultRule:
    variation: enabled
  experimentation:
    start: 2020-01-01T00:00:00Z
    end: 2020-02-01T00:00:00Z
`)
	metrics, err := NewMetrics()
	require.NoError(t, err)
	require.NoError(t, metrics.RegisterFlagLoaded(fakeFlagsetSource{
		flagsets: map[string]*ffclient.GoFeatureFlag{fixtures.FlagsetDefault: client},
	}))

	expected := `
# HELP semaphore_flag_loaded Flags currently loaded in the relay proxy cache, one series per flag with value 1.
# TYPE semaphore_flag_loaded gauge
semaphore_flag_loaded{disabled="true",flag_name="scheduled-off",flagset="default"} 1
semaphore_flag_loaded{disabled="false",flag_name="scheduled-back-on",flagset="default"} 1
semaphore_flag_loaded{disabled="false",flag_name="future-off",flagset="default"} 1
semaphore_flag_loaded{disabled="true",flag_name="experiment-over",flagset="default"} 1
semaphore_flag_loaded{disabled="false",flag_name="percentage-with-bucketing-key",flagset="default"} 1
semaphore_flag_loaded{disabled="true",flag_name="disabled-with-nested-bucketing-key",flagset="default"} 1
semaphore_flag_loaded{disabled="false",flag_name="percentage-with-nested-bucketing-key",flagset="default"} 1
semaphore_flag_loaded{disabled="true",flag_name="scheduled-off-with-bucketing-key",flagset="default"} 1
semaphore_flag_loaded{disabled="true",flag_name="experiment-over-with-nested-bucketing-key",flagset="default"} 1
`
	assert.NoError(t, testutil.GatherAndCompare(metrics.Registry, strings.NewReader(expected), semaphoreFlagLoaded))
}

func TestFlagLoaded_DeletedFlagDisappearsAfterRefresh(t *testing.T) {
	t.Parallel()

	client, path := newFileClient(t, flagsYAML)
	metrics, err := NewMetrics()
	require.NoError(t, err)
	require.NoError(t, metrics.RegisterFlagLoaded(fakeFlagsetSource{
		flagsets: map[string]*ffclient.GoFeatureFlag{fixtures.FlagsetDefault: client},
	}))
	count, err := testutil.GatherAndCount(metrics.Registry, semaphoreFlagLoaded)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	require.NoError(t, os.WriteFile(path, []byte(`
flag-on:
  variations:
    enabled: true
  defaultRule:
    variation: enabled
`), 0o600))
	require.True(t, client.ForceRefresh())

	expected := `
# HELP semaphore_flag_loaded Flags currently loaded in the relay proxy cache, one series per flag with value 1.
# TYPE semaphore_flag_loaded gauge
semaphore_flag_loaded{disabled="false",flag_name="flag-on",flagset="default"} 1
`
	assert.NoError(t, testutil.GatherAndCompare(metrics.Registry, strings.NewReader(expected), semaphoreFlagLoaded))
}

func TestFlagLoaded_NoSeriesWhenFlagsetsUnavailable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		source     FlagsetSource
		name       string
		wantSeries int
	}{
		{name: "source error", source: fakeFlagsetSource{err: errNoFlagsetsConfigured}, wantSeries: 0},
		{
			name:       "nil flagset",
			source:     fakeFlagsetSource{flagsets: map[string]*ffclient.GoFeatureFlag{fixtures.FlagsetDefault: nil}},
			wantSeries: 0,
		},
		{
			name:       "cache not initialized",
			source:     fakeFlagsetSource{flagsets: map[string]*ffclient.GoFeatureFlag{fixtures.FlagsetDefault: {}}},
			wantSeries: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, err := NewMetrics()
			require.NoError(t, err)
			require.NoError(t, m.RegisterFlagLoaded(tt.source))
			count, err := testutil.GatherAndCount(m.Registry, semaphoreFlagLoaded)
			require.NoError(t, err)
			assert.Equal(t, tt.wantSeries, count)
		})
	}
}

func TestIsEffectivelyDisabled_FallsBackToRawStateOnEvaluationError(t *testing.T) {
	t.Parallel()

	// No defaultRule makes the evaluation fail after the scheduled step re-enabled the flag.
	targetFlag := &flagdef.InternalFlag{
		Variations: &map[string]*any{"enabled": new(any(true))},
		Disable:    new(true),
		Scheduled: &[]flagdef.ScheduledStep{{
			Disable: new(false),
			Date:    new(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)),
		}},
	}
	assert.True(t, isEffectivelyDisabled(flagdef.NewService(), "no-default-rule", targetFlag))
}

func TestFlagLoaded_RegisterIsNoopWithoutRegistryOrSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		source         FlagsetSource
		wantErr        error
		name           string
		withRegistry   bool
		wantRegistered bool
	}{
		{name: "no registry", withRegistry: false, source: fakeFlagsetSource{}, wantErr: nil},
		{name: "no source", withRegistry: true, source: nil, wantErr: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			metrics := &Metrics{}

			if tt.withRegistry {
				created, err := NewMetrics()
				require.NoError(t, err)

				metrics = created
			}

			before := metricFamilyNames(t, metrics)

			assert.Equal(t, tt.wantErr, metrics.RegisterFlagLoaded(tt.source))
			assert.Equal(t, before, metricFamilyNames(t, metrics), "nothing must be registered")
			assert.Equal(t, tt.wantRegistered, slices.Contains(metricFamilyNames(t, metrics), semaphoreFlagLoaded))
		})
	}
}

// metricFamilyNames returns the sorted names of the metric families gathered from the registry of metrics.
func metricFamilyNames(t *testing.T, metrics *Metrics) []string {
	t.Helper()

	if metrics.Registry == nil {
		return nil
	}

	families, err := metrics.Registry.Gather()
	require.NoError(t, err)

	var names []string
	for _, f := range families {
		names = append(names, f.GetName())
	}

	slices.Sort(names)

	return names
}

func TestFlagLoaded_SkipsSeriesWithInvalidLabelValues(t *testing.T) {
	t.Parallel()

	valid, _ := newFileClient(t, flagsYAML)
	invalid, _ := newFileClient(t, flagsYAML)
	metrics, err := NewMetrics()
	require.NoError(t, err)
	require.NoError(t, metrics.RegisterFlagLoaded(fakeFlagsetSource{
		flagsets: map[string]*ffclient.GoFeatureFlag{"team-a": valid, "bad-\xff": invalid},
	}))

	expected := `
# HELP semaphore_flag_loaded Flags currently loaded in the relay proxy cache, one series per flag with value 1.
# TYPE semaphore_flag_loaded gauge
semaphore_flag_loaded{disabled="false",flag_name="flag-on",flagset="team-a"} 1
semaphore_flag_loaded{disabled="true",flag_name="flag-off",flagset="team-a"} 1
`

	assert.NotPanics(t, func() {
		assert.NoError(
			t,
			testutil.GatherAndCompare(metrics.Registry, strings.NewReader(expected), semaphoreFlagLoaded),
		)
	})
}
