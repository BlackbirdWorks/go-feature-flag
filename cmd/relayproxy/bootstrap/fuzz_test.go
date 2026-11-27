package bootstrap_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// FuzzParse checks that no input panics the decoder and that every failure is a path-addressed error.
func FuzzParse(f *testing.F) {
	example, err := os.ReadFile("testdata/bootstrap.yaml")
	require.NoError(f, err)

	for _, seed := range []string{
		string(example), "", v2Doc, v1Doc, "- a\n", "version: 2\nconnections: [a]\n",
		"version: 2\nconnections:\n  a: &x {type: s3, region: r}\n  b: *x\n",
		"version: 2\nauth: {adminKeys: [{fromEnv: A, fromFile: B}]}\n",
		"version: 2\nserver: {listeners: [{name: a, address: \":1\", protocols: [h2]}]}\n",
		"{{{{", "version: 2\n---\nversion: 3\n",
		"a: &a [*a]\n", "version: 2\nconnections:\n  x: {type: valkey}\n",
	} {
		f.Add([]byte(seed))
	}

	opts := options(envMap{"A": "a"}, fileMap{"B": "b"}, true)

	f.Fuzz(func(t *testing.T, data []byte) {
		cfg, parseErr := bootstrap.Parse(data, nil, opts)
		if parseErr != nil {
			require.Nil(t, cfg)
			require.NotEmpty(t, pathErrors(parseErr))

			return
		}

		require.Equal(t, bootstrap.Version, cfg.Version)
	})
}
