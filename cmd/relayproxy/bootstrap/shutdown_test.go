package bootstrap_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// The shutdown settings: the file, then the variable over it; a value that is not a positive duration, or is above
// the bound, is refused with its source.
func TestShutdownSettings(t *testing.T) {
	t.Parallel()

	const base = "version: 2\nconnections:\n  c: {type: file, path: /x}\ncontrolPlane:\n  connection: c\n" +
		"  manifest: {key: m.yaml}\n"

	tests := []struct {
		env             envMap
		name            string
		doc             string
		wantErr         string
		wantDrainDelay  time.Duration
		wantTimeout     time.Duration
		wantDrainDelayN bool
		wantTimeoutN    bool
	}{
		{name: "unset", doc: base},
		{
			name:            "from the file",
			wantDrainDelayN: true,
			wantDrainDelay:  20 * time.Second,
			wantTimeout:     time.Minute,
			wantTimeoutN:    true,
			doc:             base + "server:\n  shutdown: {drainDelay: 20s, timeout: 1m}\n",
		},
		{
			name: "variables win over the file", wantDrainDelayN: true, wantDrainDelay: 5 * time.Second,
			wantTimeout: 30 * time.Second, wantTimeoutN: true,
			doc: base + "server:\n  shutdown: {drainDelay: 20s, timeout: 1m}\n",
			env: envMap{
				bootstrap.EnvShutdownDrainDelay: "5s", bootstrap.EnvShutdownTimeout: "30s",
			},
		},
		{
			name:    "a variable that is not a duration",
			doc:     base,
			env:     envMap{bootstrap.EnvShutdownTimeout: "soon"},
			wantErr: "server.shutdown.timeout: invalid duration: set by environment variable SEMAPHORE_SHUTDOWN_TIMEOUT",
		},
		{
			name: "a zero variable", doc: base, env: envMap{bootstrap.EnvShutdownDrainDelay: "0s"},
			wantErr: "server.shutdown.drainDelay: invalid duration: set by environment variable " +
				"SEMAPHORE_SHUTDOWN_DRAIN_DELAY",
		},
		{
			name: "a drain delay above the bound", doc: base + "server:\n  shutdown: {drainDelay: 11m}\n",
			wantErr: "server.shutdown.drainDelay (line 8): invalid value: expected at most 10m0s",
		},
		{
			name: "a timeout above the bound from a variable", doc: base,
			env:     envMap{bootstrap.EnvShutdownTimeout: "1h"},
			wantErr: "server.shutdown.timeout: invalid value: set by environment variable SEMAPHORE_SHUTDOWN_TIMEOUT",
		},
		{
			name: "a negative file value", doc: base + "server:\n  shutdown: {timeout: -5s}\n",
			wantErr: "invalid duration",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := bootstrap.Parse([]byte(tt.doc), nil, options(tt.env, nil, false))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)

			shutdown := cfg.Server.Shutdown
			if tt.wantDrainDelayN {
				require.NotNil(t, shutdown.DrainDelay)
				assert.Equal(t, tt.wantDrainDelay, time.Duration(*shutdown.DrainDelay))
			} else {
				assert.Nil(t, shutdown.DrainDelay)
			}

			if tt.wantTimeoutN {
				require.NotNil(t, shutdown.Timeout)
				assert.Equal(t, tt.wantTimeout, time.Duration(*shutdown.Timeout))
			} else {
				assert.Nil(t, shutdown.Timeout, "unset stays unset: the relay applies the default")
			}
		})
	}
}
