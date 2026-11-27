package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

// TestMain pins the clock once for the whole package.
func TestMain(m *testing.M) {
	clocktest.Freeze()
	m.Run()
}

const (
	configXYaml = "--config=/x.yaml"
	configFlag  = "--config"
	unknownFlag = "--nope"
	versionFlag = "--version"
	helpFlag    = "--help"

	// unknownFlagText is what the parser says of an unknown flag.
	unknownFlagText = "unknown flag"
)

func TestParseCommandLineHelpAndVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantHelp    bool
		wantVersion bool
		wantErr     bool
	}{
		{name: "no argument"},
		{name: "long version", args: []string{versionFlag}, wantVersion: true},
		{name: "short version", args: []string{"-v"}, wantVersion: true},
		{name: "version after the config flag", args: []string{configXYaml, versionFlag}, wantVersion: true},
		{name: "config only", args: []string{configXYaml}},
		{name: "version=false", args: []string{"--version=false", configXYaml}},
		{name: "help=false and version", args: []string{"--help=false", versionFlag}, wantVersion: true},
		{name: "help", args: []string{helpFlag}, wantHelp: true},
		{name: "short help", args: []string{"-h"}, wantHelp: true},
		{name: "help and version", args: []string{helpFlag, versionFlag}, wantHelp: true, wantVersion: true},
		{name: "version and help", args: []string{versionFlag, helpFlag}, wantHelp: true, wantVersion: true},
		{name: "unknown flag", args: []string{unknownFlag, "-v"}, wantErr: true},
		{name: "unknown flag after help", args: []string{helpFlag, unknownFlag}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseCommandLine(tt.args)
			if tt.wantErr {
				require.ErrorIs(t, err, errUsage)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantHelp, got.Help)
			assert.Equal(t, tt.wantVersion, got.Version)
		})
	}
}

func TestVersionText(t *testing.T) {
	t.Parallel()

	got := versionText()
	assert.True(t, strings.HasPrefix(got, "semaphore "), got)
	assert.Contains(t, got, "\nfips140: false\n")
}

func TestConfigValueStartingWithADash(t *testing.T) {
	t.Parallel()

	got, err := parseCommandLine([]string{configFlag, "-version.yaml"})
	require.NoError(t, err)
	assert.Equal(t, "-version.yaml", got.Overrides.Config)
	assert.False(t, got.Version)
}

func TestOverridesFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want    bootstrap.Overrides
		name    string
		args    []string
		wantErr bool
	}{
		{name: "none", args: nil, want: bootstrap.Overrides{}},
		{name: "config", args: []string{configFlag, "/a.yaml"}, want: bootstrap.Overrides{Config: "/a.yaml"}},
		{name: "log level", args: []string{"--log-level=debug"}, want: bootstrap.Overrides{LogLevel: "debug"}},
		{
			name: "log format",
			args: []string{"--log-format", logFormatText},
			want: bootstrap.Overrides{LogFormat: logFormatText},
		},
		{name: "listen", args: []string{"--listen=:8080"}, want: bootstrap.Overrides{Listen: ":8080"}},
		{
			name: "ops listen",
			args: []string{"--ops-listen=127.0.0.1:9"},
			want: bootstrap.Overrides{OpsListen: "127.0.0.1:9"},
		},
		{
			name: "manifest key",
			args: []string{"--manifest-key=m/a.yaml"},
			want: bootstrap.Overrides{ManifestKey: "m/a.yaml"},
		},
		{
			name: "state dir",
			args: []string{"--state-dir=/var/s"},
			want: bootstrap.Overrides{StateDir: "/var/s", StateDirGiven: true},
		},
		{name: "state dir given empty", args: []string{"--state-dir="}, want: bootstrap.Overrides{StateDirGiven: true}},
		{name: "an unknown flag", args: []string{unknownFlag}, wantErr: true},
		{
			name: "version is not an override",
			args: []string{versionFlag, "--listen=:1"},
			want: bootstrap.Overrides{Listen: ":1"},
		},
		{name: "a flag without its value", args: []string{"--listen"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseCommandLine(tt.args)
			if tt.wantErr {
				require.ErrorIs(t, err, errUsage)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, *got.Overrides)
		})
	}
}
