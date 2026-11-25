package config_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

// TestConfigLoader_ArrayEnvOverrides goes through the real loader: a rejected array override (loadArrayEnv
// returns an error) must leave the settings of the file untouched, a well-formed one must apply.
func TestConfigLoader_ArrayEnvOverrides(t *testing.T) {
	t.Parallel()

	const (
		topLevel = "retrievers:\n  - kind: file\n    path: /file/a.yaml\n  - kind: file\n    path: /file/b.yaml\n"
		flagsets = "flagsets:\n  - name: fs1\n    apiKeys: [k1]\n    retrievers:\n" +
			"      - kind: file\n        path: /file/fs-a.yaml\n"
	)

	fileRetrievers := []retrieverconf.RetrieverConf{
		{Kind: fixtures.KindFile, Path: "/file/a.yaml"}, {Kind: fixtures.KindFile, Path: "/file/b.yaml"},
	}
	fileFlagsetRetrievers := []retrieverconf.RetrieverConf{{Kind: fixtures.KindFile, Path: "/file/fs-a.yaml"}}

	tests := []struct {
		name               string
		file               string
		env                []string
		wantRetrievers     []retrieverconf.RetrieverConf
		wantFlagsetRetr    []retrieverconf.RetrieverConf
		wantUnmarshalError bool
	}{
		{
			name: "well-formed override of an existing entry",
			file: topLevel,
			env:  []string{"RETRIEVERS_1_PATH=/env/b.yaml"},
			wantRetrievers: []retrieverconf.RetrieverConf{
				fileRetrievers[0],
				{Kind: fixtures.KindFile, Path: "/env/b.yaml"},
			},
		},
		{
			name: "well-formed override that adds an entry",
			file: topLevel,
			env:  []string{"RETRIEVERS_2_KIND=file", "RETRIEVERS_2_PATH=/env/c.yaml"},
			wantRetrievers: append(append([]retrieverconf.RetrieverConf{}, fileRetrievers...),
				retrieverconf.RetrieverConf{Kind: fixtures.KindFile, Path: "/env/c.yaml"}),
		},
		{
			name:           "negative index is rejected, the file is unchanged",
			file:           topLevel,
			env:            []string{"RETRIEVERS_-1_PATH=/env/x.yaml"},
			wantRetrievers: fileRetrievers,
		},
		{
			name:           "no field is rejected, the file is unchanged",
			file:           topLevel,
			env:            []string{"RETRIEVERS_0=/env/x.yaml"},
			wantRetrievers: fileRetrievers,
		},
		{
			name:           "index that is not a number is rejected, the file is unchanged",
			file:           topLevel,
			env:            []string{"RETRIEVERS_X_PATH=/env/x.yaml"},
			wantRetrievers: fileRetrievers,
		},
		{
			name:               "scalar entry in the file: the override is rejected and the file keeps failing as before",
			file:               "retrievers:\n  - oops\n",
			env:                []string{"RETRIEVERS_0_PATH=/env/x.yaml"},
			wantUnmarshalError: true,
		},
		{
			name:            "flagset retriever well-formed override",
			file:            flagsets,
			env:             []string{"FLAGSETS_0_RETRIEVERS_0_PATH=/env/fs.yaml"},
			wantFlagsetRetr: []retrieverconf.RetrieverConf{{Kind: fixtures.KindFile, Path: "/env/fs.yaml"}},
		},
		{
			name:            "flagset retriever negative index is rejected, the file is unchanged",
			file:            flagsets,
			env:             []string{"FLAGSETS_0_RETRIEVERS_-1_PATH=/env/fs.yaml"},
			wantFlagsetRetr: fileFlagsetRetrievers,
		},
		{
			name:            "flagset negative index is rejected, the file is unchanged",
			file:            flagsets,
			env:             []string{"FLAGSETS_-1_NAME=other"},
			wantFlagsetRetr: fileFlagsetRetrievers,
		},
		{
			name:               "flagset retriever scalar entry: the override is rejected and the file keeps failing as before",
			file:               "flagsets:\n  - name: fs1\n    apiKeys: [k1]\n    retrievers:\n      - oops\n",
			env:                []string{"FLAGSETS_0_RETRIEVERS_0_PATH=/env/fs.yaml"},
			wantUnmarshalError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "semaphore-proxy.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tt.file), 0o600))

			flagSet := pflag.NewFlagSet(fixtures.ConfigFlagName, pflag.ContinueOnError)
			flagSet.String(fixtures.ConfigFlagName, "", locationOfYourConfig)
			require.NoError(t, flagSet.Parse([]string{configFlag + path}))

			var (
				got *config.Config
				err error
			)

			require.NotPanics(t, func() {
				got, err = config.NewConfigLoader(flagSet, slog.New(slog.DiscardHandler), fixtures.VersionOneZeroZero, false,
					withEnviron(tt.env...)).
					ToConfig()
			})

			if tt.wantUnmarshalError {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)

			if tt.wantRetrievers != nil {
				require.NotNil(t, got.Retrievers)
				assert.Equal(t, tt.wantRetrievers, *got.Retrievers)
			}

			if tt.wantFlagsetRetr != nil {
				require.Len(t, got.FlagSets, 1)
				assert.Equal(t, "fs1", got.FlagSets[0].Name)
				assert.Equal(t, []string{"k1"}, got.FlagSets[0].APIKeys)
				require.NotNil(t, got.FlagSets[0].Retrievers)
				assert.Equal(t, tt.wantFlagsetRetr, *got.FlagSets[0].Retrievers)
			}
		})
	}
}
