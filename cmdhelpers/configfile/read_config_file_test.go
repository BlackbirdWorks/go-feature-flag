package configfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/model/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	flagsGoffYaml = "flags.goff.yaml"
	varA          = "var_a"
	varB          = "var_b"
)

func TestLoadConfigFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expected           map[string]dto.DTO
		name               string
		inputFilePath      string
		configFormat       string
		defaultLocations   []string
		expectErr          bool
		useDefaultLocation bool
	}{
		{
			name:          "should load the flag file as yaml",
			inputFilePath: fixtures.FlagGoffYAMLPath,
			configFormat:  protocol.FormatYAML,
			expected: map[string]dto.DTO{
				fixtures.FlagKeyTest: {
					Variations: &map[string]*any{
						varA: new(any(true)),
						varB: new(any(false)),
					},
					DefaultRule: &flag.Rule{
						VariationResult: new(varA),
					},
					Metadata: &map[string]any{
						fixtures.DescriptionField: fixtures.SimpleFeatureFlagDescription,
						fixtures.DefaultValue:     false,
					},
				},
				fixtures.FlagKeyTestSecond: {
					Variations: &map[string]*any{
						varA: new(any(1)),
						varB: new(any(2)),
					},
					DefaultRule: &flag.Rule{
						VariationResult: new(varB),
					},
					Metadata: &map[string]any{
						fixtures.DefaultValue: 123,
					},
				},
			},
			expectErr: false,
		},
		{
			name:          "should load the flag file and use yaml as default parser",
			inputFilePath: fixtures.FlagGoffYAMLPath,
			expected: map[string]dto.DTO{
				fixtures.FlagKeyTest: {
					Variations: &map[string]*any{
						varA: new(any(true)),
						varB: new(any(false)),
					},
					DefaultRule: &flag.Rule{
						VariationResult: new(varA),
					},
					Metadata: &map[string]any{
						fixtures.DescriptionField: fixtures.SimpleFeatureFlagDescription,
						fixtures.DefaultValue:     false,
					},
				},
				fixtures.FlagKeyTestSecond: {
					Variations: &map[string]*any{
						varA: new(any(1)),
						varB: new(any(2)),
					},
					DefaultRule: &flag.Rule{
						VariationResult: new(varB),
					},
					Metadata: &map[string]any{
						fixtures.DefaultValue: 123,
					},
				},
			},
			expectErr: false,
		},
		{
			name:          "should load the flag file as json",
			inputFilePath: "testdata/flag.goff.json",
			configFormat:  protocol.FormatJSON,
			expected: map[string]dto.DTO{
				fixtures.FlagKeyTest: {
					Variations: &map[string]*any{
						varA: new(any(true)),
						varB: new(any(false)),
					},
					DefaultRule: &flag.Rule{
						VariationResult: new(varA),
					},
					Metadata: &map[string]any{
						fixtures.DescriptionField: fixtures.SimpleFeatureFlagDescription,
						fixtures.DefaultValue:     false,
					},
				},
				fixtures.FlagKeyTestSecond: {
					Variations: &map[string]*any{
						varA: new(any(1.0)),
						varB: new(any(2.0)),
					},
					DefaultRule: &flag.Rule{
						VariationResult: new(varB),
					},
					Metadata: &map[string]any{
						fixtures.DefaultValue: 123.0,
					},
				},
			},
			expectErr: false,
		},
		{
			name:          "should load the flag file as toml",
			inputFilePath: "testdata/flag.goff.toml",
			configFormat:  protocol.FormatTOML,
			expected: map[string]dto.DTO{
				fixtures.FlagKeyTest: {
					Variations: &map[string]*any{
						varA: new(any(true)),
						varB: new(any(false)),
					},
					DefaultRule: &flag.Rule{
						VariationResult: new(varA),
					},
					Metadata: &map[string]any{
						fixtures.DescriptionField: fixtures.SimpleFeatureFlagDescription,
						fixtures.DefaultValue:     false,
					},
				},
				fixtures.FlagKeyTestSecond: {
					Variations: &map[string]*any{
						varA: new(any(int64(1))),
						varB: new(any(int64(2))),
					},
					DefaultRule: &flag.Rule{
						VariationResult: new(varB),
					},
					Metadata: &map[string]any{
						fixtures.DefaultValue: int64(123),
					},
				},
			},
			expectErr: false,
		},
		{
			name:               "should load the flag file as yaml from a default location",
			useDefaultLocation: true,
			inputFilePath:      fixtures.FlagGoffYAMLPath,
			configFormat:       protocol.FormatYAML,
			expected: map[string]dto.DTO{
				fixtures.FlagKeyTest: {
					Variations: &map[string]*any{
						varA: new(any(true)),
						varB: new(any(false)),
					},
					DefaultRule: &flag.Rule{
						VariationResult: new(varA),
					},
					Metadata: &map[string]any{
						fixtures.DescriptionField: fixtures.SimpleFeatureFlagDescription,
						fixtures.DefaultValue:     false,
					},
				},
				fixtures.FlagKeyTestSecond: {
					Variations: &map[string]*any{
						varA: new(any(1)),
						varB: new(any(2)),
					},
					DefaultRule: &flag.Rule{
						VariationResult: new(varB),
					},
					Metadata: &map[string]any{
						fixtures.DefaultValue: 123,
					},
				},
			},
			expectErr: false,
		},
		{
			name:          "should error if file does not exist",
			inputFilePath: "testdata/does-not-exist.yaml",
			configFormat:  protocol.FormatYAML,
			expectErr:     true,
		},
		{
			name:      "should error if file does not exist in default locations",
			expectErr: true,
		},
		{
			name:          "should error if json is invalid",
			inputFilePath: "testdata/invalid.json",
			configFormat:  protocol.FormatJSON,
			expectErr:     true,
		},
		{
			name:          "should error if yaml invalid",
			inputFilePath: "testdata/invalid.yaml",
			configFormat:  protocol.FormatYAML,
			expectErr:     true,
		},
		{
			name:          "should error if toml is invalid",
			inputFilePath: "testdata/invalid.toml",
			configFormat:  protocol.FormatTOML,
			expectErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.useDefaultLocation {
				// write the config into an isolated per-test dir (auto-removed by the
				// test framework). Using t.TempDir() instead of a fixed name in the
				// shared system temp dir avoids leftovers/collisions between runs and
				// the Windows "file in use" errors from renaming an open handle.
				dir := t.TempDir()
				content, err := os.ReadFile(tt.inputFilePath)
				require.NoError(t, err)
				err = os.WriteFile(filepath.Join(dir, flagsGoffYaml), content, 0o600)
				require.NoError(t, err)

				tt.inputFilePath = ""
				tt.defaultLocations = []string{dir + "/"}
			}

			result, err := configfile.LoadConfigFile(
				tt.inputFilePath,
				tt.configFormat,
				tt.defaultLocations,
				nil,
			)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestParseConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		data      string
		format    string
		wantErr   string
		wantFlags []string
	}{
		{
			name:      protocol.FormatJSON,
			data:      `{"f":{"variations":{"a":1}}}`,
			format:    protocol.FormatJSON,
			wantFlags: []string{"f"},
		},
		{
			name:      protocol.FormatYAML,
			data:      "f:\n  variations:\n    a: 1\n",
			format:    protocol.FormatYAML,
			wantFlags: []string{"f"},
		},
		{
			name:      "unknown format defaults to yaml",
			data:      "f:\n  variations:\n    a: 1\n",
			format:    "",
			wantFlags: []string{"f"},
		},
		{name: protocol.FormatTOML, data: "[f.variations]\na = 1\n", format: "TOML", wantFlags: []string{"f"}},
		{name: "invalid json", data: `{`, format: protocol.FormatJSON, wantErr: "could not parse file (json)"},
		{name: "invalid yaml", data: "a: [", format: protocol.FormatYAML, wantErr: "could not parse file (yaml)"},
		{name: "invalid toml", data: "a = ", format: protocol.FormatTOML, wantErr: "could not parse file (toml)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := configfile.ParseConfig([]byte(tt.data), tt.format)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Len(t, got, len(tt.wantFlags))

			for _, k := range tt.wantFlags {
				assert.Contains(t, got, k)
			}
		})
	}
}

func TestLoadConfigFileDefaultNames(t *testing.T) {
	t.Parallel()

	content := "my-flag:\n  variations:\n    a: true\n    b: false\n  defaultRule:\n    variation: a\n"

	tests := []struct {
		name      string
		wantFlag  string
		files     []string
		wantError bool
	}{
		{name: "flags.semaphore", files: []string{"flags.semaphore.yaml"}, wantFlag: fixtures.FlagKeyMy},
		{name: "legacy flags.goff", files: []string{flagsGoffYaml}, wantFlag: fixtures.FlagKeyMy},
		{name: "both names", files: []string{flagsGoffYaml, "flags.semaphore.yaml"}, wantFlag: fixtures.FlagKeyMy},
		{name: "none", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir() + "/"
			for _, f := range tt.files {
				require.NoError(t, os.WriteFile(dir+f, []byte(content), 0o600))
			}

			got, err := configfile.LoadConfigFile("", protocol.FormatYAML, []string{dir}, nil)
			if tt.wantError {
				assert.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Contains(t, got, tt.wantFlag)
		})
	}
}

func TestConfigFileDefaultLocations(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		[]string{"./", "/semaphore/", "/etc/opt/semaphore/", "/goff/", "/etc/opt/goff/"},
		configfile.DefaultLocations())
}
