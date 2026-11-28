package bootstrap_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// The schema and the loader agree on the store connections. Every case where they do not is a known limitation of
// the schema, listed here: a new disagreement fails the test.
func TestStoreSchemaAndLoaderAgree(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../.schema/bootstrap-schema.json")
	require.NoError(t, err)

	checker, root, err := newSchemaChecker(raw)
	require.NoError(t, err)

	const head = "version: 2\nconnections:\n  v: "

	tests := []struct {
		name string
		doc  string
		// limitation says why the schema and the loader differ; empty when they agree
		limitation   string
		wantSchemaOK bool
		wantLoadOK   bool
	}{
		{name: "redis ok", doc: `{type: redis, addresses: ["h:1"]}`, wantSchemaOK: true, wantLoadOK: true},
		{name: "valkey ok", doc: `{type: valkey, addresses: ["h:1"]}`, wantSchemaOK: true, wantLoadOK: true},
		{name: "redis without addresses", doc: `{type: redis}`},
		{name: "valkey without addresses", doc: `{type: valkey}`},
		{name: "empty addresses", doc: `{type: redis, addresses: []}`},
		{name: "empty address", doc: `{type: redis, addresses: [""]}`},
		{name: "address with white space", doc: `{type: redis, addresses: ["redis :6379"]}`},
		{name: "IPv6 without brackets", doc: `{type: redis, addresses: ["::1:6379"]}`},
		{name: "port with a leading zero", doc: `{type: redis, addresses: ["redis:06379"]}`},
		{name: "signed port", doc: `{type: redis, addresses: ["redis:+6379"]}`},
		{name: "bracketed IPv6", doc: `{type: redis, addresses: ["[::1]:6379"]}`, wantSchemaOK: true, wantLoadOK: true},
		{
			name: "port out of range", doc: `{type: redis, addresses: ["h:99999"]}`, wantSchemaOK: true,
			limitation: "the schema does not check the range of the port",
		},
		{
			name: "credentials in the address", doc: `{type: redis, addresses: ["u:p@h:1"]}`,
			wantSchemaOK: false, wantLoadOK: false,
		},
		{name: "cluster with a database", doc: `{type: redis, addresses: ["h:1"], cluster: true, db: 1}`},
		{name: "several addresses with a database", doc: `{type: redis, addresses: ["h:1", "h:2"], db: 1}`},
		{
			name:         "database 0 of a cluster",
			doc:          `{type: redis, addresses: ["h:1", "h:2"], db: 0}`,
			wantSchemaOK: true,
			wantLoadOK:   true,
		},
		{
			name:         "single node with a database",
			doc:          `{type: redis, addresses: ["h:1"], db: 2}`,
			wantSchemaOK: true,
			wantLoadOK:   true,
		},
		{
			name: "two addresses and a single client",
			doc:  `{type: valkey, addresses: ["h:1", "h:2"], forceSingleClient: true}`,
		},
		{
			name:         "one address and a single client",
			doc:          `{type: valkey, addresses: ["h:1"], forceSingleClient: true}`,
			wantSchemaOK: true,
			wantLoadOK:   true,
		},
		{name: "ca bundle without tls", doc: `{type: redis, addresses: ["h:1"], tls: {caBundleFile: /ca.pem}}`},
		{
			name: "server name with tls off",
			doc:  `{type: redis, addresses: ["h:1"], tls: {enable: false, serverName: x}}`,
		},
		{
			name:         "ca bundle with tls",
			doc:          `{type: redis, addresses: ["h:1"], tls: {enable: true, caBundleFile: /ca.pem}}`,
			wantSchemaOK: true,
			wantLoadOK:   true,
		},
		{
			name: "negative pool size", doc: `{type: redis, addresses: ["h:1"], poolSize: 0}`,
		},
		{name: "bad pipelining", doc: `{type: valkey, addresses: ["h:1"], pipelining: never}`},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			doc := head + tcase.doc + "\n"

			var value any

			require.NoError(t, yaml.Unmarshal([]byte(doc), &value))
			assert.Equal(t, tcase.wantSchemaOK, len(checker.check("", root, value)) == 0, "schema")

			_, loadErr := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))
			assert.Equal(t, tcase.wantLoadOK, loadErr == nil, "loader")

			if tcase.limitation == "" {
				assert.Equal(t, tcase.wantSchemaOK, tcase.wantLoadOK, "no listed limitation: they must agree")
			} else {
				assert.NotEqual(t, tcase.wantSchemaOK, tcase.wantLoadOK, tcase.limitation)
			}
		})
	}
}
