package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

// ReloadOf finds the longest rule that covers a key, and tells that the admin keys apply live.
func TestReloadOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		path      string
		wantName  string
		wantFound bool
		wantLive  bool
	}{
		{name: "admin key", path: "auth.adminKeys[].fromEnv", wantFound: true, wantLive: true},
		{
			name:      "manifest key",
			path:      "controlPlane.manifest.key",
			wantFound: true,
			wantName:  flagruntime.SettingControlPlaneMan,
		},
		{
			name:      "state dir",
			path:      "controlPlane.stateDir",
			wantFound: true,
			wantName:  flagruntime.SettingControlPlaneState,
		},
		{
			name:      "listener",
			path:      "server.listeners[].address",
			wantFound: true,
			wantName:  flagruntime.SettingServerListeners,
		},
		{
			name:      "connection",
			path:      "connections.<name>.region",
			wantFound: true,
			wantName:  flagruntime.SettingConnections,
		},
		{name: "prefix of a name is not a match", path: "serverless", wantFound: false},
		{name: "container with rules below", path: "server", wantFound: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rule, found := flagruntime.ReloadOf(tt.path)

			assert.Equal(t, tt.wantFound, found)

			if tt.wantFound {
				assert.Equal(t, tt.wantLive, rule.Live)
				assert.Equal(t, tt.wantName, rule.Setting)
			}
		})
	}
}

// Every setting that needs a restart has a name, and no live setting has one.
func TestReloadabilitiesNames(t *testing.T) {
	t.Parallel()

	table := flagruntime.Reloadabilities()
	require.NotEmpty(t, table)

	for _, rule := range table {
		assert.Equal(t, rule.Live, rule.Setting == "", rule.Path)
	}
}
