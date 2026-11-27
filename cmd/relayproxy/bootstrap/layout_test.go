package bootstrap_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// The listener layouts the relay can not serve are refused by the validation that `config validate` and the relay
// share, and accepted when a tool only reads the file (SkipRelayChecks, the publish command).
func TestListenerLayoutsTheRelayCanNotServe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr  error
		name     string
		doc      string
		wantPath string
	}{
		{name: "a unix socket", doc: "version: 2\nserver:\n  unixSocketPath: /run/s.sock\n",
			wantErr: bootstrap.ErrReservedKey, wantPath: "server.unixSocketPath"},
		{name: "two api listeners", doc: "version: 2\nserver:\n  listeners:\n    - {name: a, address: \":1\"}\n" +
			"    - {name: b, address: \":2\"}\n", wantErr: bootstrap.ErrInvalidValue, wantPath: "server.listeners[1].role"},
		{name: "two ops listeners", doc: "version: 2\nserver:\n  listeners:\n    - {name: a, address: \":1\"}\n" +
			"    - {name: b, address: \":2\", role: ops}\n    - {name: c, address: \":3\", role: ops}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "server.listeners[2].role"},
		{
			name: "ops on another host",
			doc: "version: 2\nserver:\n  listeners:\n    - {name: a, address: \"0.0.0.0:1\"}\n" +
				"    - {name: b, address: \"127.0.0.1:2\", role: ops}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: pathListener1Address,
		},
		{name: "port zero", doc: "version: 2\nserver:\n  listeners:\n    - {name: a, address: \":0\"}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "server.listeners[0].address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := bootstrap.Parse([]byte(tt.doc), nil, options(nil, nil, false))
			require.Error(t, err)
			assert.NotNil(t, findError(t, err, tt.wantErr, tt.wantPath))

			skip := options(nil, nil, false)
			skip.SkipRelayChecks = true

			_, err = bootstrap.Parse([]byte(tt.doc), nil, skip)
			require.NoError(t, err, "a tool that only reads the file does not serve it")
		})
	}
}
