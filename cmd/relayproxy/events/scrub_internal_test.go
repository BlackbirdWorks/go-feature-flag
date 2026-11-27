package events

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
)

const secretEight = "abcdefgh"

const encodedMessage = "h [REDACTED]: remote refused"

// echoExporter fails with an error that carries the text it was built with, like an HTTP client that names the
// URL it called.
type echoExporter struct{ err error }

func (echoExporter) IsBulk() bool { return true }

func (e echoExporter) Export(context.Context, *slog.Logger, []exporter.ExportableEvent) error {
	return e.err
}

var errRemote = errors.New("remote refused")

func newCounter(inner exporter.Exporter, secrets ...string) *countingExporter {
	return &countingExporter{
		inner: inner, instance: &instanceStats{}, name: &nameStats{}, secrets: scrubForms(secrets),
	}
}

// TestCountingExporter_ScrubsEveryFormOfASecret checks that the message of an error hides a secret in each form
// it can take, and that the cause stays reachable.
func TestCountingExporter_ScrubsEveryFormOfASecret(t *testing.T) {
	t.Parallel()

	const (
		token = "tok en/with+chars&<html>\"quoted\""
		short = "abc123"
		pair  = "svc-user:p4ss/word+7"
	)

	b64 := func(enc *base64.Encoding, value string) string { return enc.EncodeToString([]byte(value)) }

	tests := []struct {
		name    string
		err     error
		want    string
		secrets []string
	}{
		{name: "raw", err: fmt.Errorf("post %s: %w", token, errRemote), want: "post [REDACTED]: remote refused"},
		{
			name: "query escaped", err: fmt.Errorf("post ?t=%s: %w", url.QueryEscape(token), errRemote),
			want: "post ?t=[REDACTED]: remote refused",
		},
		{
			name: "path escaped", err: fmt.Errorf("post /%s: %w", url.PathEscape(token), errRemote),
			want: "post /[REDACTED]: remote refused",
		},
		{
			name: "json escaped", err: fmt.Errorf(`body {"k":%q}: %w`, token, errRemote),
			want: `body {"k":"[REDACTED]"}: remote refused`,
		},
		{
			name: "json escaped without html escaping",
			err:  fmt.Errorf(`body {"k":"%s"}: %w`, jsonEscaped(token, false), errRemote),
			want: `body {"k":"[REDACTED]"}: remote refused`,
		},
		{
			name: "base64 standard padded", err: fmt.Errorf("h %s: %w", b64(base64.StdEncoding, token), errRemote),
			want: encodedMessage,
		},
		{
			name: "base64 standard unpadded", err: fmt.Errorf("h %s: %w", b64(base64.RawStdEncoding, token), errRemote),
			want: encodedMessage,
		},
		{
			name: "base64 url padded", err: fmt.Errorf("h %s: %w", b64(base64.URLEncoding, token), errRemote),
			want: encodedMessage,
		},
		{
			name: "base64 url unpadded", err: fmt.Errorf("h %s: %w", b64(base64.RawURLEncoding, token), errRemote),
			want: encodedMessage,
		},
		{
			name: "basic header of a user and password pair", secrets: []string{pair},
			err:  fmt.Errorf("Authorization: Basic %s: %w", b64(base64.StdEncoding, pair), errRemote),
			want: "Authorization: Basic [REDACTED]: remote refused",
		},
		{
			name: "two secrets in one message", secrets: []string{token, "second-secret-value"},
			err:  fmt.Errorf("a %s b second-secret-value: %w", token, errRemote),
			want: "a [REDACTED] b [REDACTED]: remote refused",
		},
		{name: "no secret in the message", err: errRemote, want: "remote refused"},
		{
			name: "a secret shorter than 8 characters is not looked for", secrets: []string{short},
			err: fmt.Errorf("post %s: %w", short, errRemote), want: "post abc123: remote refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			secrets := tt.secrets
			if secrets == nil {
				secrets = []string{token}
			}

			counter := newCounter(echoExporter{err: tt.err}, secrets...)

			err := counter.Export(context.Background(), nil, nil)
			require.Error(t, err)
			assert.Equal(t, tt.want, err.Error())
			require.ErrorIs(t, err, errRemote, "the cause is still reachable")
			assert.Equal(t, uint64(1), counter.name.failures.Load())
		})
	}
}

func TestCountingExporter_PrintsNothing(t *testing.T) {
	t.Parallel()

	counter := newCounter(echoExporter{}, "a-secret-of-some-length")

	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		assert.Equal(t, "[REDACTED]", fmt.Sprintf(format, counter), format)
	}
}

func TestScrubForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		secrets []string
		want    []string
	}{
		{name: "none", secrets: nil, want: nil},
		{name: "short ones give nothing", secrets: []string{"1234567"}, want: nil},
		{
			// the raw form (the escapes are the same string) and the base64 forms that differ: standard padded,
			// standard unpadded; the URL alphabet gives the same strings
			name: "a plain 8 character secret", secrets: []string{secretEight},
			want: []string{"YWJjZGVmZ2g=", "YWJjZGVmZ2g", secretEight},
		},
		{
			name: "the longest forms come first", secrets: []string{secretEight, "abcdefghi"},
			want: []string{"YWJjZGVmZ2g=", "YWJjZGVmZ2hp", "YWJjZGVmZ2g", "abcdefghi", secretEight},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, scrubForms(tt.secrets))
		})
	}
}

// newEvent returns an event to collect.
func newEvent() *exporter.FeatureEvent { return &exporter.FeatureEvent{Kind: "feature", Key: "k"} }
