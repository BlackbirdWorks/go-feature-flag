package bootstrap_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

func secretOf(t *testing.T, doc string) bootstrap.Secret {
	t.Helper()

	var s bootstrap.Secret

	require.NoError(t, yaml.Unmarshal([]byte(doc), &s))

	return s
}

func TestSecretResolve(t *testing.T) {
	t.Parallel()

	env := envMap{"TOKEN": "from-env"}
	files := fileMap{"/run/token": "from-file\n"}

	tests := []struct {
		wantErr    error
		opts       bootstrap.LoadOptions
		name       string
		doc        string
		want       string
		wantInline bool
	}{
		{name: "env", doc: "{fromEnv: TOKEN}", opts: options(env, files, false), want: "from-env"},
		{name: "file", doc: "{fromFile: /run/token}", opts: options(env, files, false), want: "from-file"},
		{name: "inline", doc: "plain", opts: options(env, files, false), want: "plain", wantInline: true},
		{
			name:    "env missing",
			doc:     "{fromEnv: NOPE}",
			opts:    options(env, files, false),
			wantErr: bootstrap.ErrSecretUnresolved,
		},
		{
			name:    "no environment",
			doc:     "{fromEnv: TOKEN}",
			opts:    bootstrap.LoadOptions{},
			wantErr: bootstrap.ErrSecretUnresolved,
		},
		{
			name:    "file missing",
			doc:     "{fromFile: /none}",
			opts:    options(env, files, false),
			wantErr: bootstrap.ErrSecretUnresolved,
		},
		{
			name:    noReader,
			doc:     "{fromFile: /run/token}",
			opts:    bootstrap.LoadOptions{},
			wantErr: bootstrap.ErrNoReader,
		},
		{name: "unset", doc: "null", opts: options(env, files, false), wantErr: bootstrap.ErrSecretUnresolved},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			s := secretOf(t, tcase.doc)
			assert.Equal(t, tcase.wantInline, s.IsInline())

			got, err := s.Resolve(tcase.opts)
			if tcase.wantErr != nil {
				require.ErrorIs(t, err, tcase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tcase.want, got.Reveal())
		})
	}
}

func TestSecretRejectsBadForms(t *testing.T) {
	t.Parallel()

	for _, doc := range []string{"{}", "{fromEnv: A, fromFile: B}", "{other: A}", "[a]", "{fromEnv: ''}"} {
		t.Run(doc, func(t *testing.T) {
			t.Parallel()

			var s bootstrap.Secret

			require.ErrorIs(t, yaml.Unmarshal([]byte(doc), &s), bootstrap.ErrInvalidSecret)
		})
	}
}

func TestResolvedSecretNeverPrints(t *testing.T) {
	t.Parallel()

	const sentinel = "SENTINEL-resolved-value"

	r, err := secretOf(t, "{fromEnv: T}").Resolve(options(envMap{"T": sentinel}, nil, false))
	require.NoError(t, err)

	asYAML, err := yaml.Marshal(r)
	require.NoError(t, err)

	asJSON, err := json.Marshal(r)
	require.NoError(t, err)

	text, err := r.MarshalText()
	require.NoError(t, err)

	var logs bytes.Buffer

	slog.New(slog.NewJSONHandler(&logs, nil)).Info("resolved", slog.Any("secret", r))

	tests := []struct {
		name string
		got  string
	}{
		{name: "String", got: r.String()},
		{name: "%v", got: r.String()},
		{name: fmtPlus, got: fmt.Sprintf(fmtPlus, r)},
		{name: fmtGo, got: fmt.Sprintf(fmtGo, r)},
		{name: "%s", got: r.String()},
		{name: "%q", got: fmt.Sprintf("%q", r)},
		{name: "MarshalText", got: string(text)},
		{name: "YAML", got: string(asYAML)},
		{name: "JSON", got: string(asJSON)},
		{name: "slog", got: logs.String()},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			assert.Contains(t, tcase.got, bootstrap.RedactedValue)
			assert.NotContains(t, tcase.got, sentinel)
		})
	}

	assert.Equal(t, sentinel, r.Reveal())
}
