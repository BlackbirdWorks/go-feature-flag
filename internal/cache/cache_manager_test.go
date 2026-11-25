package cache_test

import (
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/internal/notification"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/modules/core/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/testhelpers/mock"
)

const (
	trueVar                  = "true_var"
	updateCacheErrorVWantErr = "UpdateCache() error = %v, wantErr %v"
)

func Test_FlagCacheNotInit(t *testing.T) {
	t.Parallel()

	fCache := cache.New(nil, "", nil)
	fCache.Close()
	got, err := fCache.GetFlag(fixtures.FlagKeyTest)
	assert.Nil(t, got)
	assert.EqualError(t, err, "impossible to read the flag before the initialisation")
}

func Test_GetFlagNotExist(t *testing.T) {
	t.Parallel()

	fCache := cache.New(nil, "", nil)
	got, err := fCache.GetFlag("not-exists-flag")
	assert.Equal(t, &flagdef.InternalFlag{}, got)
	assert.EqualError(t, err, "flag [not-exists-flag] does not exists")
}

func Test_FlagCache(t *testing.T) {
	t.Parallel()

	yamlFile := []byte(`
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
    variation: false_var	
  trackEvents: false`)

	jsonFile := []byte(`{
  "test-flag": {
    "variations": {
      "true_var": true,
      "false_var": false
    },
    "targeting": [
      {
        "query": "key eq \"random-key\"",
        "percentage": {
          "true_var": 100,
          "false_var": 0
        }
      }
    ],
    "defaultRule": {
      "variation": "false_var"
    },
		"trackEvents": false
  }
}
	`)

	tomlFile := []byte(`[test-flag]
trackEvents = false

  [test-flagdef.variations]
  true_var = true
  false_var = false

  [[test-flagdef.targeting]]
  query = 'key eq "random-key"'

    [test-flagdef.targeting.percentage]
    true_var = 100.00
    false_var = 0.00

  [test-flagdef.defaultRule]
  variation = "false_var"`)

	type args struct {
		loadedFlags []byte
	}

	tests := []struct {
		expected   map[string]flagdef.InternalFlag
		name       string
		flagFormat string
		args       args
		wantErr    bool
	}{
		{
			name:       "Yaml valid",
			flagFormat: protocol.FormatYAML,
			args: args{
				loadedFlags: yamlFile,
			},
			expected: map[string]flagdef.InternalFlag{
				fixtures.FlagKeyTest: {
					Rules: &[]flagdef.Rule{
						{
							Query: new(fixtures.QueryKeyEqRandomKey),
							Percentages: &map[string]float64{
								fixtures.FalseVar: 0,
								trueVar:           100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.FalseVar: new(any(false)),
						trueVar:           new(any(true)),
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new(fixtures.FalseVar),
					},
					TrackEvents: new(false),
				},
			},
			wantErr: false,
		},
		{
			name:       "Yaml invalid file",
			flagFormat: protocol.FormatYAML,
			args: args{
				loadedFlags: []byte(`
test-flag:
  variations:
    true_var: true
    false_var: false
  targeting:
    - query: key eq "random-key"
      percentage: "toto"
  defaultRule:
    variation: false_var	
  trackEvents: false`),
			},
			wantErr: true,
		},
		{
			name: "JSON valid",
			args: args{
				loadedFlags: jsonFile,
			},
			flagFormat: protocol.FormatJSON,
			expected: map[string]flagdef.InternalFlag{
				fixtures.FlagKeyTest: {
					Rules: &[]flagdef.Rule{
						{
							Query: new(fixtures.QueryKeyEqRandomKey),
							Percentages: &map[string]float64{
								fixtures.FalseVar: 0,
								trueVar:           100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.FalseVar: new(any(false)),
						trueVar:           new(any(true)),
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new(fixtures.FalseVar),
					},
					TrackEvents: new(false),
				},
			},
			wantErr: false,
		},
		{
			name:       "JSON invalid file",
			flagFormat: protocol.FormatJSON,
			args: args{
				loadedFlags: []byte(`{
  "test-flag": {
    "variations": {
      "true_var": true,
      "false_var": false
    },
    "targeting": [
      {
        "query": "key eq \"random-key\"",
        "percentage": "toto"
      }
    ],
    "defaultRule": {
      "variation": "false_var"
    },
    "trackEvents": false
  }
}`),
			},
			wantErr: true,
		},
		{
			name: "TOML valid",
			args: args{
				loadedFlags: tomlFile,
			},
			flagFormat: protocol.FormatTOML,
			expected: map[string]flagdef.InternalFlag{
				fixtures.FlagKeyTest: {
					Rules: &[]flagdef.Rule{
						{
							Query: new(fixtures.QueryKeyEqRandomKey),
							Percentages: &map[string]float64{
								fixtures.FalseVar: 0,
								trueVar:           100,
							},
						},
					},
					Variations: &map[string]*any{
						fixtures.FalseVar: new(any(false)),
						trueVar:           new(any(true)),
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new(fixtures.FalseVar),
					},
					TrackEvents: new(false),
				},
			},
			wantErr: false,
		},
		{
			name: "TOML invalid file",
			args: args{
				loadedFlags: []byte(`
[test-flag]
trackEvents = false
[test-flagdef.variations]
true_var = true
false_var = false
[[test-flagdef.targeting]]
query = 'key eq "random-key"'
percentage = "toto
[test-flagdef.defaultRule]
variation = "false_var"
`),
			},
			flagFormat: protocol.FormatTOML,
			wantErr:    true,
		},
		{
			name: "TOML nested deeper than the parser limit",
			args: args{
				loadedFlags: []byte("a = " + strings.Repeat("{b=", 20000) + "1" + strings.Repeat("}", 20000)),
			},
			flagFormat: protocol.FormatTOML,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fCache := cache.New(notification.NewService([]notifier.Notifier{}), "",
				slog.Default())

			newFlags, err := cache.ConvertToFlagStruct(tt.args.loadedFlags, tt.flagFormat)
			if tt.wantErr {
				assert.Error(t, err)

				return
			}

			err = fCache.UpdateCache(newFlags, nil, true)
			if tt.wantErr {
				assert.Error(t, err, updateCacheErrorVWantErr, err, tt.wantErr)

				return
			}

			require.NoError(t, err, updateCacheErrorVWantErr, err, tt.wantErr)
			// If no error we compare with expected
			for key, expected := range tt.expected {
				got, _ := fCache.GetFlag(key)
				assert.Equal(
					t,
					&expected,
					got,
				)
			}

			fCache.Close()
		})
	}
}

func Test_AllFlags(t *testing.T) {
	t.Parallel()

	yamlFile := []byte(`
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
    variation: false_var	
  trackEvents: false`)

	type args struct {
		loadedFlags []byte
	}

	tests := []struct {
		expected   map[string]flagdef.InternalFlag
		name       string
		flagFormat string
		args       args
		wantErr    bool
	}{
		{
			name:       "Yaml valid",
			flagFormat: protocol.FormatYAML,
			args: args{
				loadedFlags: yamlFile,
			},
			expected: map[string]flagdef.InternalFlag{
				fixtures.FlagKeyTest: {
					Variations: &map[string]*any{
						fixtures.FalseVar: new(any(false)),
						trueVar:           new(any(true)),
					},
					Rules: &[]flagdef.Rule{
						{
							Query: new(fixtures.QueryKeyEqRandomKey),
							Percentages: &map[string]float64{
								fixtures.FalseVar: 0,
								trueVar:           100,
							},
						},
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new(fixtures.FalseVar),
					},
					TrackEvents: new(false),
				},
			},
			wantErr: false,
		},
		{
			name:       "Yaml multiple flags",
			flagFormat: protocol.FormatYAML,
			args: args{
				loadedFlags: []byte(`
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
    variation: false_var	
  trackEvents: false

test-flag2:
  variations:
    true_var: "true"
    false_var: "false"
  targeting:
    - query: key eq "random-key"
      percentage:
        true_var: 0
        false_var: 100
  defaultRule:
    variation: false_var  
  trackEvents: false
`),
			},
			expected: map[string]flagdef.InternalFlag{
				fixtures.FlagKeyTest: {
					Variations: &map[string]*any{
						fixtures.FalseVar: new(any(false)),
						trueVar:           new(any(true)),
					},
					Rules: &[]flagdef.Rule{
						{
							Query: new(fixtures.QueryKeyEqRandomKey),
							Percentages: &map[string]float64{
								fixtures.FalseVar: 0,
								trueVar:           100,
							},
						},
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new(fixtures.FalseVar),
					},
					TrackEvents: new(false),
				},
				"test-flag2": {
					Variations: &map[string]*any{
						fixtures.FalseVar: new(any(fixtures.FalseLiteral)),
						trueVar:           new(any(fixtures.TrueLiteral)),
					},
					Rules: &[]flagdef.Rule{
						{
							Query: new(fixtures.QueryKeyEqRandomKey),
							Percentages: &map[string]float64{
								fixtures.FalseVar: 100,
								trueVar:           0,
							},
						},
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new(fixtures.FalseVar),
					},
					TrackEvents: new(false),
				},
			},
			wantErr: false,
		},
		{
			name:       "empty",
			flagFormat: protocol.FormatYAML,
			args: args{
				loadedFlags: []byte(``),
			},
			expected: map[string]flagdef.InternalFlag{},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fCache := cache.New(notification.NewService([]notifier.Notifier{}), "", nil)

			newFlags, err := cache.ConvertToFlagStruct(tt.args.loadedFlags, tt.flagFormat)
			if tt.wantErr {
				assert.Error(t, err)

				return
			}

			err = fCache.UpdateCache(newFlags, slog.Default(), true)
			require.NoError(t, err)

			allFlags, err := fCache.AllFlags()
			if tt.wantErr {
				assert.Error(t, err, updateCacheErrorVWantErr, err, tt.wantErr)

				return
			}

			require.NoError(t, err)

			// If no error we compare with expected
			for key, expected := range tt.expected {
				got := allFlags[key]
				assert.Equal(
					t,
					&expected,
					got,
				)
			}

			fCache.Close()
		})
	}
}

func Test_cacheManagerImpl_GetLatestUpdateDate(t *testing.T) {
	t.Parallel()

	loadedFlags := []byte(`test-flag:
  variations:
    true_var: true
    false_var: false
  targeting:
    - query: key eq "random-key"
      percentage:
        true_var: 100
        false_var: 0
  defaultRule:
    variation: false_var	
  trackEvents: false
`)

	fCache := cache.New(notification.NewService([]notifier.Notifier{}), "", nil)
	timeBefore := fCache.GetLatestUpdateDate()
	newFlags, _ := cache.ConvertToFlagStruct(loadedFlags, protocol.FormatYAML)
	_ = fCache.UpdateCache(newFlags, slog.Default(), true)
	timeAfter := fCache.GetLatestUpdateDate()

	assert.True(t, timeBefore.Before(timeAfter))
}

func Test_persistCacheAndRestartCacheWithIt(t *testing.T) {
	t.Parallel()

	file, err := os.CreateTemp(t.TempDir(), "")
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	// We start without any cache and we persist it on disk
	loadedFlags := []byte(`test-flag:
  variations:
    true_var: true
    false_var: false
  targeting:
    - query: key eq "random-key"
      percentage:
        true_var: 100
        false_var: 0
  defaultRule:
    variation: false_var	
  trackEvents: false
`)
	loadedFlagsMap := map[string]dto.DTO{}
	err = yaml.Unmarshal(loadedFlags, &loadedFlagsMap)
	require.NoError(t, err)

	fCache := cache.New(notification.NewService([]notifier.Notifier{}), file.Name(), nil)
	err = fCache.UpdateCache(loadedFlagsMap, slog.Default(), true)
	require.NoError(t, err)
	allFlags1, err := fCache.AllFlags()
	require.NoError(t, err)

	// the go routine writes the file: wait until it holds the flag
	require.Eventually(t, func() bool {
		written, readErr := os.ReadFile(file.Name())

		persisted := map[string]dto.DTO{}

		return readErr == nil && yaml.Unmarshal(written, &persisted) == nil && len(persisted) == len(loadedFlagsMap)
	}, 30*time.Second, 5*time.Millisecond, "the cache file was never written")

	// we start a new cache with the file persisted
	fCache2 := cache.New(notification.NewService([]notifier.Notifier{}), "", nil)
	content, err := os.ReadFile(file.Name())
	require.NoError(t, err)

	loadedFlagsMap2 := map[string]dto.DTO{}
	err = yaml.Unmarshal(content, &loadedFlagsMap2)
	require.NoError(t, err)
	require.Len(t, loadedFlagsMap2, 1)
	err = fCache2.UpdateCache(loadedFlagsMap2, slog.Default(), true)
	require.NoError(t, err)
	allFlags2, err := fCache2.AllFlags()
	require.NoError(t, err)

	// Compare the 2 caches
	assert.Equal(t, allFlags1, allFlags2)
}

func TestCacheManager_UpdateCache(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		initialFlags map[string]dto.DTO
		updatedFlags map[string]dto.DTO
		wantFlags    []string
	}{
		{
			name: "Update existing flags",
			initialFlags: map[string]dto.DTO{
				fixtures.FlagKeyOne: {
					Variations: &map[string]*any{},
					DefaultRule: &flagdef.Rule{
						VariationResult: new(fixtures.TrueLiteral),
					},
				},
			},
			updatedFlags: map[string]dto.DTO{
				fixtures.FlagKeyOne: {
					Variations: &map[string]*any{
						fixtures.TrueLiteral: new(any(true)),
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new(fixtures.TrueLiteral),
					},
				},
				fixtures.FlagKeyTwo: {
					Variations: &map[string]*any{
						fixtures.FalseLiteral: new(any(false)),
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new(fixtures.FalseLiteral),
					},
				},
			},
			wantFlags: []string{fixtures.FlagKeyOne, fixtures.FlagKeyTwo},
		},
		{
			name:         "Empty initial flags",
			initialFlags: map[string]dto.DTO{},
			updatedFlags: map[string]dto.DTO{
				fixtures.FlagKeyOne: {
					Variations: &map[string]*any{
						fixtures.TrueLiteral: new(any(true)),
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new(fixtures.TrueLiteral),
					},
				},
			},
			wantFlags: []string{fixtures.FlagKeyOne},
		},
		{
			name: "Remove a flag",
			initialFlags: map[string]dto.DTO{
				fixtures.FlagKeyOne: {
					Variations: &map[string]*any{
						fixtures.TrueLiteral: new(any(true)),
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new(fixtures.TrueLiteral),
					},
				},
				fixtures.FlagKeyTwo: {
					Variations: &map[string]*any{
						fixtures.FalseLiteral: new(any(false)),
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new(fixtures.FalseLiteral),
					},
				},
			},
			updatedFlags: map[string]dto.DTO{
				fixtures.FlagKeyOne: {
					Variations: &map[string]*any{
						fixtures.TrueLiteral: new(any(true)),
					},
					DefaultRule: &flagdef.Rule{
						VariationResult: new(fixtures.TrueLiteral),
					},
				},
			},
			wantFlags: []string{fixtures.FlagKeyOne},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Test UpdateCache without notification
			mockNotificationService := &mock.NotificationService{}
			cacheMgr := cache.New(
				mockNotificationService,
				"",
				slog.Default(),
			)

			err := cacheMgr.UpdateCache(tt.initialFlags, nil, false)
			require.NoError(t, err)

			err = cacheMgr.UpdateCache(tt.updatedFlags, nil, false)
			require.NoError(t, err)
			assert.Equal(
				t,
				0,
				mockNotificationService.GetNotifyCalls(),
				"Notify should not be called for UpdateCache",
			)

			flags, err := cacheMgr.AllFlags()
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.wantFlags, slices.Collect(maps.Keys(flags)),
				"Cache should hold the updated flags")

			// Test UpdateCacache with notification
			mockNotificationService = &mock.NotificationService{}
			cacheMgr = cache.New(
				mockNotificationService,
				"",
				slog.Default(),
			)

			err = cacheMgr.UpdateCache(tt.initialFlags, nil, false)
			require.NoError(t, err)

			err = cacheMgr.UpdateCache(tt.updatedFlags, nil, true)
			require.NoError(t, err)
			assert.Equal(
				t,
				1,
				mockNotificationService.GetNotifyCalls(),
				"Notify should be called once for UpdateCache with notification",
			)

			flags, err = cacheMgr.AllFlags()
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.wantFlags, slices.Collect(maps.Keys(flags)),
				"Cache should hold the updated flags")
		})
	}
}
