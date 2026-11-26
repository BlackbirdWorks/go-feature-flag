package bootstrap_test

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

const (
	fuzzCanary    = "CANARY-fuzz-b7e2"
	fuzzResolved  = "CANARY-resolved-4c19"
	fuzzMarker    = "@@"
	fuzzMaxDepth  = 64
	listItem      = "[]"
	anyName       = "*"
	envReference  = "fromEnv"
	fileReference = "fromFile"
)

// position is what a scalar of the document is.
type position int

const (
	// positionPlain is an ordinary value, a key or anything not listed: it is printed as it is.
	positionPlain position = iota
	// positionValue is a secret value written inline: it must never be printed.
	positionValue
	// positionReference is the text of a fromEnv or fromFile reference: the name of a variable or a path, which is
	// printed by design.
	positionReference
)

// secretPositions is the one table of the places where a secret value is written, by exact path. A "*" matches
// one name (a connection, an exporter, a notifier, a header) and "[]" one item of a list. Everything not listed
// is plain, whatever the names on its path are.
func secretPositions() [][]string {
	table := make([][]string, 0, 17)

	table = append(table, []string{tcAuth, tcAdminkeys, listItem})

	for _, field := range []string{tcAccesskeyid, tcSecretaccesskey, tcSessiontoken, tcExternalid} {
		table = append(table, []string{tcConnections, anyName, tcCredentials, field})
	}

	for _, kind := range []string{tcExporters, tcNotifiers} {
		for _, field := range []string{endpointURLKey, tcSecret, tcGooglecredentials, tcAccountkey, "webhookUrl"} {
			table = append(table, []string{kind, anyName, field})
		}

		table = append(table, []string{kind, anyName, tcHeaders, anyName})
	}

	return table
}

func pathMatches(pattern, trail []string) bool {
	if len(pattern) != len(trail) {
		return false
	}

	for i := range pattern {
		if pattern[i] != anyName && pattern[i] != trail[i] {
			return false
		}
	}

	return true
}

// classify says what a scalar at the path of keys is.
func classify(trail []string) position {
	for _, pattern := range secretPositions() {
		if pathMatches(pattern, trail) {
			return positionValue
		}
	}

	if len(trail) > 0 && (trail[len(trail)-1] == envReference || trail[len(trail)-1] == fileReference) {
		for _, pattern := range secretPositions() {
			if pathMatches(pattern, trail[:len(trail)-1]) {
				return positionReference
			}
		}
	}

	return positionPlain
}

func hasCanary(node *yaml.Node) bool {
	return strings.Contains(node.Value, fuzzCanary) || strings.Contains(node.Tag, fuzzCanary)
}

// canaryOnlyInValues reports whether the canary appears in the document only as a secret value. It is false when
// the canary is also a key, a reference or an ordinary value: those may be printed.
func canaryOnlyInValues(node *yaml.Node, trail []string, depth int) bool {
	if node.Kind == yaml.AliasNode {
		node = node.Alias
	}

	if depth > fuzzMaxDepth {
		return false
	}

	if node.Kind == yaml.ScalarNode {
		return !hasCanary(node) || classify(trail) == positionValue
	}

	for i, child := range node.Content {
		childTrail := trail

		switch {
		case node.Kind == yaml.SequenceNode:
			childTrail = append(slices.Clone(trail), listItem)
		case node.Kind == yaml.MappingNode && i%2 == 0:
			if hasCanary(child) {
				return false
			}

			continue
		case node.Kind == yaml.MappingNode:
			childTrail = append(slices.Clone(trail), node.Content[i-1].Value)
		}

		if !canaryOnlyInValues(child, childTrail, depth+1) {
			return false
		}
	}

	return true
}

func hasValue(node *yaml.Node, depth int) bool {
	if node.Kind == yaml.AliasNode {
		node = node.Alias
	}

	if depth > fuzzMaxDepth {
		return false
	}

	if node.Kind == yaml.ScalarNode {
		return hasCanary(node)
	}

	return slices.ContainsFunc(node.Content, func(child *yaml.Node) bool { return hasValue(child, depth+1) })
}

// checkCanaryDoc parses the document (the marker stands for the canary) and checks that the canary, when it is
// only a secret value, is in no error, no print and no formatting of the configuration. It returns false when
// the document was skipped because the canary is also somewhere it may be printed. The canary that the injected
// environment and reader return must never appear at all.
func checkCanaryDoc(tb testing.TB, data []byte, inline bool) bool {
	tb.Helper()

	doc := []byte(strings.ReplaceAll(string(data), fuzzMarker, fuzzCanary))

	var tree yaml.Node

	parsed := yaml.Unmarshal(doc, &tree) == nil
	valueOnly := parsed && hasValue(&tree, 0) && canaryOnlyInValues(&tree, nil, 0)

	opts := bootstrap.LoadOptions{
		LookupEnv:          func(string) (string, bool) { return fuzzResolved, true },
		ReadFile:           func(string) ([]byte, error) { return []byte(fuzzResolved), nil },
		AllowInlineSecrets: inline,
	}

	cfg, err := bootstrap.Parse(doc, nil, opts)
	if err != nil {
		if valueOnly {
			require.NotContains(tb, err.Error(), fuzzCanary)
		}

		require.NotContains(tb, err.Error(), fuzzResolved)

		return valueOnly
	}

	var printed bytes.Buffer

	require.NoError(tb, cfg.Print(&printed))
	require.NotContains(tb, printed.String(), fuzzResolved)
	require.NotContains(tb, fmt.Sprintf("%+v", cfg), fuzzResolved)

	for _, key := range cfg.Auth.AdminKeys {
		resolved, resolveErr := key.Resolve(opts)
		if resolveErr == nil {
			require.NotContains(tb, fmt.Sprintf("%v %+v %#v %s", resolved, resolved, resolved, resolved), fuzzResolved)
		}
	}

	if valueOnly {
		require.NotContains(tb, printed.String(), fuzzCanary)
		require.NotContains(tb, fmt.Sprintf("%+v", cfg), fuzzCanary)
	}

	return valueOnly
}

// canaryValueSeeds are documents where the canary is only a secret value, one per kind of place.
func canaryValueSeeds() []string {
	return []string{
		"version: 2\nauth: {adminKeys: [@@, @@]}\n",
		"version: 2\nconnections:\n  a: {type: s3, credentials: {type: static, accessKeyId: @@, secretAccessKey: @@}}\n",
		"version: 2\nconnections:\n  a: {type: s3, credentials: {type: assumeRole, roleArn: r, externalId: @@}}\n",
		"version: 2\nconnections:\n  a: {type: s3, " +
			"credentials: {type: static, accessKeyId: x, secretAccessKey: x, sessionToken: @@}}\n",
		"version: 2\nnotifiers:\n  n: {kind: webhook, endpointUrl: @@, secret: @@, headers: {A: @@}}\n",
		"version: 2\nnotifiers:\n  n: {kind: slack, webhookUrl: @@}\n",
		"version: 2\nexporters:\n  e: {kind: azureBlobStorage, accountName: a, container: c, accountKey: @@}\n",
		"version: 2\nexporters:\n  e: {kind: googleStorage, bucket: b, googleCredentials: @@}\n",
		"version: 2\nexporters:\n  e: {kind: webhook, endpointUrl: @@, headers: {A: @@, B: @@}}\n",
		"version: 2\nconnections:\n  meta: {type: s3, credentials: {type: static, accessKeyId: @@, secretAccessKey: @@}}\n",
		"version: 2\nnotifiers:\n  meta: {kind: webhook, endpointUrl: @@, secret: @@}\n",
		"version: 2\nexporters:\n  meta: {kind: webhook, endpointUrl: @@, headers: {A: @@}}\n",
		"version: 2\nexporters:\n  headers: {kind: webhook, endpointUrl: @@, headers: {headers: @@, meta: @@}}\n",
		"version: 2\nnotifiers:\n  secret: {kind: webhook, endpointUrl: @@, secret: @@}\n",
		"version: 2\nconnections:\n  credentials: {type: s3, " +
			"credentials: {type: static, accessKeyId: @@, secretAccessKey: @@}}\n",
	}
}

// canaryReferenceSeeds are documents where the canary is the text of a reference or an ordinary value. The check
// does not assert on the canary there, it still asserts on the values the injected readers return.
func canaryReferenceSeeds() []string {
	return []string{
		"version: 2\nauth: {adminKeys: [{fromEnv: @@}]}\n",
		"version: 2\nauth: {adminKeys: [{fromFile: @@}]}\n",
		"version: 2\nconnections:\n  a: {type: s3, " +
			"credentials: {type: static, accessKeyId: {fromFile: @@}, secretAccessKey: {fromEnv: @@}}}\n",
		"version: 2\nexporters:\n  e: {kind: log, meta: {secret: @@}}\n",
		"version: 2\nnotifiers:\n  meta: {kind: webhook, endpointUrl: {fromFile: @@}}\n",
		"version: 2\nexporters:\n  e: {kind: webhook, endpointUrl: {fromEnv: U}, headers: {A: [@@]}}\n",
	}
}

func TestCanarySeedsAreExercised(t *testing.T) {
	t.Parallel()

	for _, seed := range canaryValueSeeds() {
		t.Run(seed, func(t *testing.T) {
			t.Parallel()

			assert.Contains(t, seed, fuzzMarker)
			assert.True(t, checkCanaryDoc(t, []byte(seed), false), "exercised without inline secrets")
			assert.True(t, checkCanaryDoc(t, []byte(seed), true), "exercised with inline secrets")
		})
	}

	for _, seed := range canaryReferenceSeeds() {
		t.Run(seed, func(t *testing.T) {
			t.Parallel()

			assert.False(t, checkCanaryDoc(t, []byte(seed), false), "the canary is not a secret value there")
			assert.False(t, checkCanaryDoc(t, []byte(seed), true), "the canary is not a secret value there")
		})
	}
}

func TestClassifyPositions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path []string
		want position
	}{
		{name: "admin key", path: []string{tcAuth, tcAdminkeys, "[]"}, want: positionValue},
		{name: "admin key reference", path: []string{tcAuth, tcAdminkeys, "[]", envReference}, want: positionReference},
		{
			name: "admin key file reference",
			path: []string{tcAuth, tcAdminkeys, "[]", "fromFile"},
			want: positionReference,
		},
		{name: "a key under auth", path: []string{tcAuth, "other"}, want: positionPlain},
		{name: "access key", path: []string{tcConnections, "a", tcCredentials, tcAccesskeyid}, want: positionValue},
		{name: "role ARN", path: []string{tcConnections, "a", tcCredentials, tcRolearn}, want: positionPlain},
		{name: "external id", path: []string{tcConnections, "a", tcCredentials, tcExternalid}, want: positionValue},
		{
			name: "credentials reference",
			path: []string{tcConnections, "a", tcCredentials, tcAccesskeyid, "fromFile"},
			want: positionReference,
		},
		{name: tcBucket, path: []string{tcConnections, "a", tcBucket}, want: positionPlain},
		{
			name: "connection named meta",
			path: []string{tcConnections, tcMeta, tcCredentials, tcSecretaccesskey},
			want: positionValue,
		},
		{
			name: "connection named credentials",
			path: []string{tcConnections, tcCredentials, tcCredentials, tcSessiontoken},
			want: positionValue,
		},
		{
			name: "connection named auth",
			path: []string{tcConnections, tcAuth, tcCredentials, tcAccesskeyid},
			want: positionValue,
		},
		{name: "connection named secret", path: []string{tcConnections, tcSecret, tcBucket}, want: positionPlain},
		{name: "exporter endpoint", path: []string{tcExporters, "e", endpointURLKey}, want: positionValue},
		{name: "exporter named meta", path: []string{tcExporters, tcMeta, endpointURLKey}, want: positionValue},
		{name: "exporter named headers", path: []string{tcExporters, tcHeaders, tcHeaders, "A"}, want: positionValue},
		{name: "exporter named secret", path: []string{tcExporters, tcSecret, tcSecret}, want: positionValue},
		{name: "exporter account key", path: []string{tcExporters, "e", tcAccountkey}, want: positionValue},
		{
			name: "exporter google credentials",
			path: []string{tcExporters, "e", tcGooglecredentials},
			want: positionValue,
		},
		{name: "exporter header", path: []string{tcExporters, "e", tcHeaders, "A"}, want: positionValue},
		{name: "exporter header named meta", path: []string{tcExporters, "e", tcHeaders, tcMeta}, want: positionValue},
		{
			name: "exporter header reference",
			path: []string{tcExporters, "e", tcHeaders, "A", envReference},
			want: positionReference,
		},
		{name: "exporter header list", path: []string{tcExporters, "e", tcHeaders, "A", "[]"}, want: positionPlain},
		{name: "exporter metadata", path: []string{tcExporters, "e", tcMeta, tcSecret}, want: positionPlain},
		{name: "exporter metadata headers", path: []string{tcExporters, "e", tcMeta, tcHeaders}, want: positionPlain},
		{name: "exporter path", path: []string{tcExporters, "e", tcPath}, want: positionPlain},
		{name: "notifier webhook URL", path: []string{tcNotifiers, "n", "webhookUrl"}, want: positionValue},
		{name: "notifier named credentials", path: []string{tcNotifiers, tcCredentials, tcSecret}, want: positionValue},
		{name: "notifier header", path: []string{tcNotifiers, "n", tcHeaders, "Authorization"}, want: positionValue},
		{name: "notifier metadata", path: []string{tcNotifiers, "n", tcMeta, endpointURLKey}, want: positionPlain},
		{name: "listener address", path: []string{"server", "listeners", "[]", "address"}, want: positionPlain},
		{name: "secret named listener", path: []string{"server", tcSecret}, want: positionPlain},
		{name: "empty path", path: nil, want: positionPlain},
		{name: "too deep", path: []string{tcAuth, tcAdminkeys, "[]", "x", "y"}, want: positionPlain},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tcase.want, classify(tcase.path))
		})
	}
}
