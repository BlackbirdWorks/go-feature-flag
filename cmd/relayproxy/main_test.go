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
)

func TestVersionRequested(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "no argument", args: nil, want: false},
		{name: "long flag", args: []string{versionFlag}, want: true},
		{name: "short flag", args: []string{"-v"}, want: true},
		{name: "after the config flag", args: []string{configXYaml, versionFlag}, want: true},
		{name: "config only", args: []string{configXYaml}, want: false},
		{name: "unknown flags are ignored", args: []string{unknownFlag, "-v"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, versionRequested(tt.args))
		})
	}
}

func TestVersionText(t *testing.T) {
	t.Parallel()

	got := versionText()
	assert.True(t, strings.HasPrefix(got, "semaphore "), got)
	assert.Contains(t, got, "\nfips140: false\n")
}

func TestConfigFlagSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		wantConfig  string
		args        []string
		wantVersion bool
	}{
		{name: "config only", args: []string{configXYaml}, wantConfig: "/x.yaml"},
		{
			name:       "version=false before config keeps the config",
			args:       []string{"--version=false", configXYaml},
			wantConfig: "/x.yaml",
		},
		{
			name:       "config value starting with a dash is a value",
			args:       []string{configFlag, "-version.yaml"},
			wantConfig: "-version.yaml",
		},
		{name: "version", args: []string{versionFlag}, wantVersion: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newConfigFlagSet()
			require.NoError(t, f.Parse(tt.args))
			got, err := f.GetString("config")
			require.NoError(t, err)
			assert.Equal(t, tt.wantConfig, got)
			assert.Equal(t, tt.wantVersion, versionRequested(tt.args))
		})
	}
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

			got, err := overridesFrom(tt.args)
			if tt.wantErr {
				require.ErrorIs(t, err, errUsage)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, *got)
		})
	}
}
