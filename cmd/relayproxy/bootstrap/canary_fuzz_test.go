package bootstrap_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// canaryFor derives the canary of an input from the input itself: the digest of the input cannot be written in
// the input, with or without YAML escapes, so wherever the canary shows up in an output it came from a resolved
// secret.
func canaryFor(data []byte) string {
	sum := sha256.Sum256(data)

	return "CANARY-" + hex.EncodeToString(sum[:])[:32]
}

// resolvedOptions resolves every variable and file to the canary, except the override variables, which are
// validated settings.
func resolvedOptions(canary string, inline bool) bootstrap.LoadOptions {
	return bootstrap.LoadOptions{
		LookupEnv: func(name string) (string, bool) {
			if isOverrideName(name) {
				return "", false
			}

			return canary, true
		},
		ReadFile:           func(string) ([]byte, error) { return []byte(canary), nil },
		AllowInlineSecrets: inline,
	}
}

// canaryReport is what checkResolvedCanary reached.
type canaryReport struct {
	err      error
	loaded   bool
	resolved int
}

// checkResolvedCanary loads the document and checks that the canary derived from it is in no error, no print, no
// formatting and no encoding of the loaded configuration, and in no resolve error. Every secret of the loaded
// configuration is resolved: a reference must reveal the canary, which proves that the checks had something to
// leak. It does not look at the structure of the input.
func checkResolvedCanary(tb testing.TB, doc []byte, inline, allowResolveErrors bool) canaryReport {
	tb.Helper()

	canary := canaryFor(doc)
	opts := resolvedOptions(canary, inline)

	cfg, err := bootstrap.Parse(doc, nil, opts)
	if err != nil {
		require.NotContains(tb, err.Error(), canary)

		return canaryReport{err: err}
	}

	var printed bytes.Buffer

	require.NoError(tb, cfg.Print(&printed))
	require.NotContains(tb, printed.String(), canary)

	for _, verb := range []string{"%v", fmtPlus, fmtGo, "%s"} {
		require.NotContains(tb, fmt.Sprintf(verb, cfg), canary)
	}

	asJSON, err := json.Marshal(cfg)
	require.NoError(tb, err)
	require.NotContains(tb, string(asJSON), canary)

	asYAML, err := yaml.Marshal(cfg)
	require.NoError(tb, err)
	require.NotContains(tb, string(asYAML), canary)

	var held []heldSecret

	collectSecrets(reflect.ValueOf(cfg), reflect.ValueOf(cfg).Elem(), "", &held)

	report := canaryReport{loaded: true}

	for _, item := range held {
		resolved, resolveErr := item.secret.Resolve(opts)
		if resolveErr != nil {
			require.NotContains(tb, resolveErr.Error(), canary)

			// a document that loaded resolves, since the environment and the reader answer every name except the
			// override variables: a failure is only expected where the case says so
			require.True(tb, allowResolveErrors, "a secret of a loaded document did not resolve: %v", resolveErr)

			continue
		}

		report.resolved++

		if !item.secret.IsInline() {
			require.Equal(tb, canary, resolved.Reveal(), "a reference reveals what the environment or the file holds")
		}

		for _, verb := range []string{"%v", fmtPlus, fmtGo, "%s"} {
			require.NotContains(tb, fmt.Sprintf(verb, resolved), canary)
		}
	}

	return report
}

// aliasBomb is a document that is small and expands enormously when its aliases are followed, with the sentinel
// of the error the loader must return.
type aliasBomb struct {
	want error
	doc  string
}

func aliasBombs() []aliasBomb {
	return []aliasBomb{
		{bootstrap.ErrUnknownKey, "version: 2\nx: &a [*a, *a]\n"},
		{bootstrap.ErrUnknownKey, "version: 2\nx: &a {k: *a, l: *a}\n"},
		{bootstrap.ErrUnknownKey, "version: 2\nl1: &a [x, x, x]\nl2: &b [*a, *a, *a]\n" +
			"l3: &c [*b, *b, *b, *b, *b, *b, *b, *b, *b]\nl4: &d [*c, *c, *c, *c, *c, *c, *c, *c, *c]\n" +
			"l5: &e [*d, *d, *d, *d, *d, *d, *d, *d, *d]\nl6: &f [*e, *e, *e, *e, *e, *e, *e, *e, *e]\n"},
		{bootstrap.ErrMissingValue, "version: 2\nconnections: &a {b: *a, c: *a}\n"},
		{bootstrap.ErrSyntax, "version: 2\nserver: {listeners: &a [*a, *a]}\n"},
		{bootstrap.ErrInvalidSecret, "version: 2\nauth: {adminKeys: &a [*a, *a]}\n"},
		{bootstrap.ErrUnknownKey, "version: 2\nm: &m {<<: *m}\nconnections:\n  a: {<<: *m, type: s3, region: r}\n"},
		{
			bootstrap.ErrUnknownKey,
			"version: 2\nbase: &base {type: s3, region: r, bucket: b}\nconnections:\n  a: *base\n  b: *base\n  c: {<<: *base}\n",
		},
		{bootstrap.ErrInvalidValue, "version: 2\nexporters: &e {a: &x {kind: log, meta: &y {k: *y}}, b: *x}\n"},
	}
}

// TestAliasBombsReturn checks that the loader returns the expected error on documents that expand without bound
// when their aliases are followed. The deadline is the one of the test.
func TestAliasBombsReturn(t *testing.T) {
	t.Parallel()

	for _, bomb := range aliasBombs() {
		t.Run(bomb.doc, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()

			result := make(chan error, 1)

			go func() {
				_, err := bootstrap.Parse([]byte(bomb.doc), nil, options(nil, nil, false))
				result <- err
			}()

			select {
			case err := <-result:
				require.ErrorIs(t, err, bomb.want)
			case <-ctx.Done():
				assert.Fail(t, "the loader did not return on the document", bomb.doc)
			}
		})
	}
}

var canaryTokenRE = regexp.MustCompile(`CANARY-[A-Za-z0-9]+`)

// TestMalformedDocumentsNeverEchoTheirCanary writes sentinels inline in documents that a syntax error could
// quote, and checks that none of them is in the error. Each case lists every sentinel its document contains.
func TestMalformedDocumentsNeverEchoTheirCanary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		doc      string
		canaries []string
	}{
		{"unknown alias", "version: 2\nauth: {adminKeys: [*CANARY-m1]}\n", []string{"CANARY-m1"}},
		{"unterminated list", "version: 2\nauth: {adminKeys: [CANARY-m2\n", []string{"CANARY-m2"}},
		{
			"anchor on a scalar then alias",
			"version: 2\nauth: {adminKeys: [*CANARY-m3, &CANARY-m3 x]}\n",
			[]string{"CANARY-m3"},
		},
		{
			"open quote",
			"version: 2\nconnections:\n  a: {type: s3, region: r, bucket: \"CANARY-m4}\n",
			[]string{"CANARY-m4"},
		},
		{"tab as indentation", "version: 2\n\tCANARY-m5: x\n", []string{"CANARY-m5"}},
		{
			"malformed second document",
			"version: 2\nauth: {adminKeys: [CANARY-m6]}\n---\n[CANARY-m6\n",
			[]string{"CANARY-m6"},
		},
		{"bad escape", "version: 2\nauth: {adminKeys: [\"CANARY-m7\\q\"]}\n", []string{"CANARY-m7"}},
		{"unknown tag", "version: 2\nauth: {adminKeys: [!!CANARY-m8 x]}\n", []string{"CANARY-m8"}},
		{
			"duplicate anchor use and an alias that is not defined",
			"version: 2\nauth: {adminKeys: [&CANARY-m9 a, &CANARY-m9 b, *CANARY-m10]}\n",
			[]string{"CANARY-m9", "CANARY-m10"},
		},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			// the guard: the document holds each listed sentinel and no other one
			assert.ElementsMatch(
				t,
				tcase.canaries,
				slices.Compact(slices.Sorted(slices.Values(canaryTokenRE.FindAllString(tcase.doc, -1)))),
			)

			for _, inline := range []bool{false, true} {
				_, err := bootstrap.Parse([]byte(tcase.doc), nil, options(nil, nil, inline))
				require.Error(t, err)

				for _, canary := range tcase.canaries {
					assert.NotContains(t, err.Error(), canary)
				}
			}
		})
	}
}

// fuzzSeed is a named document of the corpus of FuzzParseCanary, with what the check must reach on it.
type fuzzSeed struct {
	wantErr     error
	name        string
	doc         string
	wantSecrets int
	wantLoads   bool
}

func fuzzSeeds() []fuzzSeed {
	return []fuzzSeed{
		{
			name: "admin keys by reference", wantLoads: true, wantSecrets: 2,
			doc: "version: 2\nauth: {adminKeys: [{fromEnv: ADMIN_KEY}, {fromFile: /run/admin}]}\n",
		},
		{
			name: "admin keys, one inline", wantLoads: true, wantSecrets: 2,
			doc: "version: 2\nauth: {adminKeys: [plain-admin-key, {fromEnv: ADMIN_KEY}]}\n",
		},
		{
			name: "static credentials by reference", wantLoads: true, wantSecrets: 2,
			doc: "version: 2\nconnections:\n  a: {type: s3, region: r, credentials: {type: static, " +
				"accessKeyId: {fromEnv: AK}, secretAccessKey: {fromFile: /run/sk}}}\n",
		},
		{
			name:        "webhook notifier with a header",
			wantLoads:   true,
			wantSecrets: 2,
			doc:         "version: 2\nnotifiers:\n  n: {kind: webhook, endpointUrl: {fromEnv: U}, headers: {A: {fromEnv: H}}}\n",
		},
		{
			name: "webhook exporter with a secret", wantLoads: true, wantSecrets: 2,
			doc: "version: 2\nexporters:\n  e: {kind: webhook, endpointUrl: {fromFile: /u}, secret: {fromEnv: S}}\n",
		},
		{
			name: "admin key from a SEMAPHORE_ variable that is not an override", wantLoads: true, wantSecrets: 1,
			doc: "version: 2\nauth: {adminKeys: [{fromEnv: SEMAPHORE_ADMIN_KEY}]}\n",
		},
		{
			name: "invalid variable name", wantErr: bootstrap.ErrInvalidSecret,
			doc: "version: 2\nauth: {adminKeys: [{fromEnv: \"bad name\"}]}\n",
		},
		{name: "undefined alias", wantErr: bootstrap.ErrSyntax, doc: "version: 2\nauth: {adminKeys: [*unknown]}\n"},
	}
}

func TestFuzzSeedsReachTheChecks(t *testing.T) {
	t.Parallel()

	for _, seed := range fuzzSeeds() {
		t.Run(seed.name, func(t *testing.T) {
			t.Parallel()

			for _, inline := range []bool{false, true} {
				report := checkResolvedCanary(t, []byte(seed.doc), inline, false)

				assert.Equal(t, seed.wantLoads, report.loaded, "the seed loads")

				if seed.wantLoads {
					require.NoError(t, report.err)
					assert.Equal(t, seed.wantSecrets, report.resolved, "the secrets that were resolved and checked")
					assert.Positive(t, report.resolved)

					continue
				}

				require.ErrorIs(t, report.err, seed.wantErr)
			}
		})
	}
}

// FuzzParseCanary checks, for any input, that the canary derived from the input, which the injected environment and
// reader return for every secret reference, is never in an error, a print, a formatting or an encoding of the
// loader. It does not cover secrets written inline in the input: inline positions cannot be told apart in
// arbitrary input without a classifier of the input, and the one that existed was unsound. Inline secrets are
// covered by TestNoSecretPositionLeaks, which generates a document for every secret position of the configuration
// type, and by TestMalformedDocumentsNeverEchoTheirCanary.
func FuzzParseCanary(f *testing.F) {
	for _, bomb := range aliasBombs() {
		f.Add([]byte(bomb.doc))
	}

	for _, seed := range fuzzSeeds() {
		f.Add([]byte(seed.doc))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// arbitrary input may name an override variable as a reference, which does not resolve
		checkResolvedCanary(t, data, false, true)
		checkResolvedCanary(t, data, true, true)
	})
}
