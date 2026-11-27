package bootstrap_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// A state directory flag that is given empty means "no state directory" and wins over the variable and the file; a
// flag that is not given leaves them in charge.
func TestStateDirFlagGivenEmpty(t *testing.T) {
	t.Parallel()

	const fileDoc = "version: 2\nconnections:\n  c: {type: file, path: /x}\ncontrolPlane:\n  connection: c\n" +
		"  manifest: {key: m.yaml}\n  stateDir: /from-file\n"

	tests := []struct {
		env       envMap
		name      string
		flag      string
		wantDir   string
		flagGiven bool
	}{
		{name: "not given, file only", wantDir: "/from-file"},
		{name: "not given, env set", env: envMap{bootstrap.EnvStateDir: "/from-env"}, wantDir: "/from-env"},
		{name: "given empty, file set", flagGiven: true, wantDir: ""},
		{
			name:      "given empty, env and file set",
			flagGiven: true,
			env:       envMap{bootstrap.EnvStateDir: "/from-env"},
			wantDir:   "",
		},
		{name: "given a value, env and file set", flagGiven: true, flag: "/from-flag",
			env: envMap{bootstrap.EnvStateDir: "/from-env"}, wantDir: "/from-flag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := bootstrap.Parse(
				[]byte(fileDoc),
				&bootstrap.Overrides{StateDir: tt.flag, StateDirGiven: tt.flagGiven},
				options(tt.env, nil, false),
			)
			require.NoError(t, err)
			assert.Equal(t, tt.wantDir, cfg.ControlPlane.StateDir)
		})
	}
}
