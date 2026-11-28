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

// API and ops listeners that can not both bind are refused at load: the same port with a wildcard host on a side. The
// schema can not say it: it is a known limitation (the schema accepts these files).
const (
	wildcard1031 = "0.0.0.0:1031"
	loopback1031 = "127.0.0.1:1031"
)

func TestOverlappingListenersAreRefused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		api, ops string
		wantErr  bool
	}{
		{name: "IPv4 wildcard and loopback on one port", api: wildcard1031, ops: loopback1031, wantErr: true},
		{name: "loopback and IPv4 wildcard on one port", api: loopback1031, ops: wildcard1031, wantErr: true},
		{name: "empty host and loopback on one port", api: defaultAPI, ops: loopback1031, wantErr: true},
		{name: "IPv6 wildcard and IPv4 loopback (dual stack)", api: "[::]:1031", ops: loopback1031, wantErr: true},
		{name: "IPv4 wildcard and a name", api: wildcard1031, ops: "localhost:1031", wantErr: true},
		{name: "IPv4 wildcard and IPv6 loopback", api: wildcard1031, ops: "[::1]:1031"},
		{name: "two specific hosts on one port", api: loopback1031, ops: "127.0.0.2:1031"},
		{name: "distinct ports with a wildcard", api: wildcard1031, ops: "127.0.0.1:1032"},
		{name: "wildcard and wildcard on distinct ports", api: ":1031", ops: ":1032"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := "version: 2\nserver:\n  listeners:\n    - {name: a, address: \"" + tt.api + "\"}\n" +
				"    - {name: b, address: \"" + tt.ops + "\", role: ops}\n"

			_, err := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))
			if tt.wantErr {
				require.Error(t, err)
				assert.NotNil(t, findError(t, err, bootstrap.ErrInvalidValue, pathListener1Address))
				assert.Contains(t, err.Error(), "checked conservatively")

				return
			}

			require.NoError(t, err)

			skip := options(nil, nil, false)
			skip.SkipRelayChecks = true

			_, err = bootstrap.Parse([]byte(doc), nil, skip)
			require.NoError(t, err, "the layout checks are the loader's only: the schema accepts the file")
		})
	}
}

// The ops listener has an address of its own: another host than the api listener is allowed, and the same address is
// not.
func TestOpsListenerHasItsOwnAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		ops          string
		wantOpsValue string
		wantErr      bool
	}{
		{name: "loopback", ops: "127.0.0.1:2", wantOpsValue: "127.0.0.1:2"},
		{name: "every interface", ops: ":2", wantOpsValue: ":2"},
		{name: "the address of the api listener", ops: "0.0.0.0:1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := "version: 2\nserver:\n  listeners:\n    - {name: a, address: \"0.0.0.0:1\"}\n" +
				"    - {name: b, address: \"" + tt.ops + "\", role: ops}\n"

			cfg, err := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))
			if tt.wantErr {
				require.Error(t, err)
				assert.NotNil(t, findError(t, err, bootstrap.ErrDuplicateName, pathListener1Address))

				return
			}

			require.NoError(t, err)
			assert.Equal(t, "0.0.0.0:1", cfg.Server.Listeners[0].Address)
			assert.Equal(t, tt.wantOpsValue, cfg.Server.Listeners[1].Address)
		})
	}
}

// A port is written as digits, with no sign and no leading zero: the loader compares the written form and the server
// binds the number, so 01031 and 1031 would hide an overlap.
func TestListenerPortsAreWrittenPlainly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		address string
		wantErr bool
	}{
		{name: "written plainly", address: defaultAPI},
		{name: "a leading zero", address: ":01031", wantErr: true},
		{name: "a sign", address: ":+1031", wantErr: true},
		{name: "a hex form", address: ":0x407", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := "version: 2\nserver:\n  listeners:\n    - {name: a, address: \"" + tt.address + "\"}\n"

			_, err := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))
			if tt.wantErr {
				require.Error(t, err)
				assert.NotNil(t, findError(t, err, bootstrap.ErrInvalidValue, "server.listeners[0].address"))

				return
			}

			require.NoError(t, err)
		})
	}
}
