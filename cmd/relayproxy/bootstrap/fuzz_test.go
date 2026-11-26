package bootstrap_test

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// FuzzParse checks that no input panics the decoder and that every failure is a path-addressed error.
func FuzzParse(f *testing.F) {
	example, err := os.ReadFile("testdata/bootstrap.yaml")
	require.NoError(f, err)

	for _, seed := range []string{
		string(example), "", v2Doc, v1Doc, "- a\n", "version: 2\nconnections: [a]\n",
		"version: 2\nconnections:\n  a: &x {type: s3}\n  b: *x\n",
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

const (
	fuzzCanary   = "CANARY-fuzz-b7e2"
	fuzzMarker   = "@@"
	fuzzMaxDepth = 64
)

// secretKeys are the keys whose scalar value is a secret or an admin key.
func secretKeys() []string {
	return []string{
		"accessKeyId", "secretAccessKey", "sessionToken", "externalId", "endpointUrl", "webhookUrl", "secret",
		"googleCredentials", "accountKey",
	}
}

// hasCanary reports whether the scalar node holds the canary in its value or its tag.
func hasCanary(node *yaml.Node) bool {
	return strings.Contains(node.Value, fuzzCanary) || strings.Contains(node.Tag, fuzzCanary)
}

// isSecretKey reports whether the value of the key is a secret, an admin key, or a map of header secrets.
func isSecretKey(key string) bool {
	return slices.Contains(secretKeys(), key) || key == "adminKeys" || key == "headers"
}

// onlyInSecretPositions reports whether the canary appears in the document only as a scalar value of a secret
// key, an admin key, or a header value. Anywhere else it is an ordinary value that may be printed.
func onlyInSecretPositions(node *yaml.Node, secretPosition bool, depth int) bool {
	return secretPositions(node, secretPosition, false, depth)
}

// secretPositions is onlyInSecretPositions; plain is true below "meta", where every value is printed as is.
func secretPositions(node *yaml.Node, secretPosition, plain bool, depth int) bool {
	if node.Kind == yaml.AliasNode {
		node = node.Alias
	}

	if depth > fuzzMaxDepth {
		return false
	}

	if node.Kind == yaml.ScalarNode {
		return secretPosition || !hasCanary(node)
	}

	for i, child := range node.Content {
		childSecret := secretPosition
		plainBelow := plain

		if node.Kind == yaml.MappingNode {
			if i%2 == 0 {
				// a key: the canary may not be one
				if hasCanary(child) {
					return false
				}

				continue
			}

			parentKey := node.Content[i-1].Value
			childSecret = !plain && (isSecretKey(parentKey) || (secretPosition && child.Kind == yaml.MappingNode))
			plainBelow = plain || parentKey == "meta"
		}

		if !secretPositions(child, childSecret, plainBelow, depth+1) {
			return false
		}
	}

	return true
}

// FuzzParseCanary checks that a sentinel written as a secret value never appears in an error or in the print.
func FuzzParseCanary(f *testing.F) {
	for _, seed := range []string{
		"version: 2\nauth: {adminKeys: [@@, @@]}\n",
		"version: 2\nexporters:\n  e: {kind: log, meta: {secret: @@}}\n",
		"version: 2\nauth: {adminKeys: [{fromEnv: @@}]}\n",
		"version: 2\nconnections:\n  a: {type: s3, credentials: {type: static, accessKeyId: @@, secretAccessKey: @@}}\n",
		"version: 2\nconnections:\n  a: {type: s3, credentials: {type: assumeRole, roleArn: r, externalId: @@}}\n",
		"version: 2\nnotifiers:\n  n: {kind: webhook, endpointUrl: @@, secret: @@, headers: {A: @@}}\n",
		"version: 2\nexporters:\n  e: {kind: azureBlobStorage, accountName: a, container: c, accountKey: @@}\n",
		"version: 2\nexporters:\n  e: {kind: webhook, endpointUrl: {fromEnv: U}, headers: {A: [@@]}}\n",
		"version: 2\nconnections:\n  a: {type: s3, credentials: {accessKeyId: {fromFile: @@}}}\n",
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		doc := []byte(strings.ReplaceAll(string(data), fuzzMarker, fuzzCanary))

		var tree yaml.Node
		if yaml.Unmarshal(doc, &tree) == nil && !onlyInSecretPositions(&tree, false, 0) {
			return
		}

		for _, inline := range []bool{false, true} {
			cfg, err := bootstrap.Parse(doc, nil, options(nil, nil, inline))
			if err != nil {
				require.NotContains(t, err.Error(), fuzzCanary)

				continue
			}

			var printed bytes.Buffer

			require.NoError(t, cfg.Print(&printed))
			require.NotContains(t, printed.String(), fuzzCanary)
			require.NotContains(t, fmt.Sprintf("%+v", cfg), fuzzCanary)
		}
	})
}
