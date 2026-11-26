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
		"accessKeyId", "secretAccessKey", "sessionToken", "externalId", endpointURLKey, webhookURLKey, "secret",
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
	return secretPositions(node, secretPosition, nil, depth)
}

// isMetaPath reports whether the path of keys is the metadata of an exporter or a notifier: below it every value
// is printed as is. A connection, an exporter or a notifier that is merely named meta is not.
func isMetaPath(trail []string) bool {
	return len(trail) >= 3 && (trail[0] == "exporters" || trail[0] == "notifiers") && trail[2] == "meta"
}

// secretPositions is onlyInSecretPositions with the path of keys to the node.
func secretPositions(node *yaml.Node, secretPosition bool, trail []string, depth int) bool {
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
		childSecret, childTrail, isKey := secretPosition, trail, false

		if node.Kind == yaml.MappingNode {
			childSecret, childTrail, isKey = mappingChild(node, i, secretPosition, trail)
		}

		if isKey {
			// a key: the canary may not be one
			if hasCanary(child) {
				return false
			}

			continue
		}

		if !secretPositions(child, childSecret, childTrail, depth+1) {
			return false
		}
	}

	return true
}

// mappingChild tells how the i-th node of a mapping is treated: whether it is a key, and for a value whether it
// is in a secret position and its path of keys. It returns (secret, trail, isKey).
func mappingChild(node *yaml.Node, i int, secretPosition bool, trail []string) (bool, []string, bool) {
	if i%2 == 0 {
		return false, trail, true
	}

	parentKey := node.Content[i-1].Value
	child := node.Content[i]
	secret := !isMetaPath(trail) && (isSecretKey(parentKey) || (secretPosition && child.Kind == yaml.MappingNode))

	return secret, append(slices.Clone(trail), parentKey), false
}

// FuzzParseCanary checks that a sentinel written as a secret value never appears in an error or in the print.
func FuzzParseCanary(f *testing.F) {
	for _, seed := range []string{
		"version: 2\nauth: {adminKeys: [@@, @@]}\n",
		"version: 2\nexporters:\n  e: {kind: log, meta: {secret: @@}}\n",
		"version: 2\nconnections:\n  meta: {type: s3, credentials: {type: static, accessKeyId: @@, secretAccessKey: @@}}\n",
		"version: 2\nnotifiers:\n  meta: {kind: webhook, endpointUrl: @@, secret: @@}\n",
		"version: 2\nexporters:\n  meta: {kind: webhook, endpointUrl: @@, headers: {A: @@}}\n",
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
