package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// resolvedCanary is the value that the injected environment and reader return. It never appears in the input
// of a fuzz case, so wherever it shows up in an output it came from a resolved secret.
const resolvedCanary = "CANARY-resolved-4c19-value"

// resolvedOptions resolves every variable and file to the canary, except the override variables, which are
// validated settings.
func resolvedOptions(inline bool) bootstrap.LoadOptions {
	return bootstrap.LoadOptions{
		LookupEnv: func(name string) (string, bool) {
			if strings.HasPrefix(name, "SEMAPHORE_") {
				return "", false
			}

			return resolvedCanary, true
		},
		ReadFile:           func(string) ([]byte, error) { return []byte(resolvedCanary), nil },
		AllowInlineSecrets: inline,
	}
}

// checkResolvedCanary loads the document and checks that the resolved canary is in no error, no print, no
// formatting and no encoding of the loaded configuration, and in no error of a resolve. It does not look at the
// structure of the input.
func checkResolvedCanary(tb testing.TB, doc []byte, inline bool) {
	tb.Helper()

	opts := resolvedOptions(inline)

	cfg, err := bootstrap.Parse(doc, nil, opts)
	if err != nil {
		require.NotContains(tb, err.Error(), resolvedCanary)

		return
	}

	var printed bytes.Buffer

	require.NoError(tb, cfg.Print(&printed))
	require.NotContains(tb, printed.String(), resolvedCanary)

	for _, verb := range []string{"%v", fmtPlus, fmtGo, "%s"} {
		require.NotContains(tb, fmt.Sprintf(verb, cfg), resolvedCanary)
	}

	asJSON, err := json.Marshal(cfg)
	require.NoError(tb, err)
	require.NotContains(tb, string(asJSON), resolvedCanary)

	asYAML, err := yaml.Marshal(cfg)
	require.NoError(tb, err)
	require.NotContains(tb, string(asYAML), resolvedCanary)

	var held []heldSecret

	collectSecrets(reflect.ValueOf(cfg), reflect.ValueOf(cfg).Elem(), "", &held)

	for _, item := range held {
		resolved, resolveErr := item.secret.Resolve(opts)
		if resolveErr != nil {
			require.NotContains(tb, resolveErr.Error(), resolvedCanary)

			continue
		}

		for _, verb := range []string{"%v", fmtPlus, fmtGo, "%s"} {
			require.NotContains(tb, fmt.Sprintf(verb, resolved), resolvedCanary)
		}
	}
}

// aliasBombs are documents that are small and expand enormously when the aliases are followed.
func aliasBombs() []string {
	return []string{
		"version: 2\nx: &a [*a, *a]\n",
		"version: 2\nx: &a {k: *a, l: *a}\n",
		"version: 2\nl1: &a [x, x, x]\nl2: &b [*a, *a, *a]\n" +
			"l3: &c [*b, *b, *b, *b, *b, *b, *b, *b, *b]\nl4: &d [*c, *c, *c, *c, *c, *c, *c, *c, *c]\n" +
			"l5: &e [*d, *d, *d, *d, *d, *d, *d, *d, *d]\nl6: &f [*e, *e, *e, *e, *e, *e, *e, *e, *e]\n",
		"version: 2\nconnections: &a {b: *a, c: *a}\n",
		"version: 2\nserver: {listeners: &a [*a, *a]}\n",
		"version: 2\nauth: {adminKeys: &a [*a, *a]}\n",
		"version: 2\nm: &m {<<: *m}\nconnections:\n  a: {<<: *m, type: s3}\n",
		"version: 2\nbase: &base {type: s3, bucket: b}\nconnections:\n  a: *base\n  b: *base\n  c: {<<: *base}\n",
		"version: 2\nexporters: &e {a: &x {kind: log, meta: &y {k: *y}}, b: *x}\n",
	}
}

// TestAliasBombsReturn checks that the loader returns on documents that expand without bound when their aliases
// are followed. The deadline is the one of the test.
func TestAliasBombsReturn(t *testing.T) {
	t.Parallel()

	for _, doc := range aliasBombs() {
		t.Run(doc, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()

			done := make(chan struct{})

			go func() {
				defer close(done)

				cfg, err := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))
				assert.True(t, err != nil || cfg != nil)
			}()

			select {
			case <-done:
			case <-ctx.Done():
				assert.Fail(t, "the loader did not return on the document", doc)
			}
		})
	}
}

// TestMalformedDocumentsNeverEchoTheirCanary writes a canary inline in documents that are not valid YAML, where
// a syntax error could quote it, and checks that it is not in the error.
func TestMalformedDocumentsNeverEchoTheirCanary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		canary string
		doc    string
	}{
		{"unknown alias", "CANARY-m1", "version: 2\nauth: {adminKeys: [*CANARY-m1]}\n"},
		{"unterminated list", "CANARY-m2", "version: 2\nauth: {adminKeys: [CANARY-m2\n"},
		{"anchor on a scalar then alias", "CANARY-m3", "version: 2\nauth: {adminKeys: [*CANARY-m3, &CANARY-m3 x]}\n"},
		{"open quote", "CANARY-m4", "version: 2\nconnections:\n  a: {type: s3, bucket: \"CANARY-m4}\n"},
		{"tab as indentation", "CANARY-m5", "version: 2\n\tCANARY-m5: x\n"},
		{"malformed second document", "CANARY-m6", "version: 2\nauth: {adminKeys: [CANARY-m6]}\n---\n[CANARY-m6\n"},
		{"bad escape", "CANARY-m7", "version: 2\nauth: {adminKeys: [\"CANARY-m7\\q\"]}\n"},
		{"unknown tag", "CANARY-m8", "version: 2\nauth: {adminKeys: [!!CANARY-m8 x]}\n"},
		{
			"duplicate anchor use",
			"CANARY-m9",
			"version: 2\nauth: {adminKeys: [&CANARY-m9 a, &CANARY-m9 b, *CANARY-m10]}\n",
		},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			require.Contains(t, tcase.doc, tcase.canary)

			for _, inline := range []bool{false, true} {
				_, err := bootstrap.Parse([]byte(tcase.doc), nil, options(nil, nil, inline))
				require.Error(t, err)
				assert.NotContains(t, err.Error(), tcase.canary)
			}
		})
	}
}

// FuzzParseCanary checks, for any input, that the value returned by the injected environment and reader is
// never in an error, a print, a formatting or an encoding.
func FuzzParseCanary(f *testing.F) {
	seeds := append(aliasBombs(),
		"version: 2\nauth: {adminKeys: [{fromEnv: ADMIN_KEY}, {fromFile: /run/admin}]}\n",
		"version: 2\nconnections:\n  a: {type: s3, credentials: {type: static, "+
			"accessKeyId: {fromEnv: AK}, secretAccessKey: {fromFile: /run/sk}}}\n",
		"version: 2\nnotifiers:\n  n: {kind: webhook, endpointUrl: {fromEnv: U}, headers: {A: {fromEnv: H}}}\n",
		"version: 2\nexporters:\n  e: {kind: webhook, endpointUrl: {fromFile: /u}, secret: {fromEnv: S}}\n",
		"version: 2\nauth: {adminKeys: [{fromEnv: \"bad name\"}]}\n",
		"version: 2\nauth: {adminKeys: [*unknown]}\n",
	)

	for _, seed := range seeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if bytes.Contains(data, []byte(resolvedCanary)) {
			t.Skip("the input contains the canary")
		}

		checkResolvedCanary(t, data, false)
		checkResolvedCanary(t, data, true)
	})
}
