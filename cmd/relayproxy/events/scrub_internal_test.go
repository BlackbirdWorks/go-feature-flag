package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
)

type failingExporter struct{ err error }

func (failingExporter) IsBulk() bool { return true }

func (f failingExporter) Export(context.Context, *slog.Logger, []exporter.ExportableEvent) error {
	return f.err
}

var errRemote = errors.New("remote refused")

// TestCountingExporter_ScrubsSecretsFromErrors checks that the message of an error hides the secrets the
// exporter was built with, in the raw and the escaped forms, and that the cause stays reachable.
func TestCountingExporter_ScrubsSecretsFromErrors(t *testing.T) {
	t.Parallel()

	const token = "tok en/with+chars"

	tests := []struct {
		err     error
		name    string
		want    string
		secrets []string
	}{
		{
			name:    "raw",
			err:     fmt.Errorf("post %s: %w", token, errRemote),
			secrets: []string{token},
			want:    "post [REDACTED]: remote refused",
		},
		{
			name: "query escaped", err: fmt.Errorf("post ?t=%s: %w", url.QueryEscape(token), errRemote),
			secrets: []string{token}, want: "post ?t=[REDACTED]: remote refused",
		},
		{
			name: "path escaped", err: fmt.Errorf("post /%s: %w", url.PathEscape(token), errRemote),
			secrets: []string{token}, want: "post /[REDACTED]: remote refused",
		},
		{name: "no secret in the message", err: errRemote, secrets: []string{token}, want: "remote refused"},
		{
			name: "no secrets known",
			err:  fmt.Errorf("post %s: %w", token, errRemote),
			want: "post " + token + ": remote refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stats := &exporterStats{}
			wrapper := &countingExporter{inner: failingExporter{err: tt.err}, stats: stats, secrets: tt.secrets}

			err := wrapper.Export(context.Background(), nil, nil)
			require.Error(t, err)
			assert.Equal(t, tt.want, err.Error())
			require.ErrorIs(t, err, errRemote, "the cause is still reachable")
			assert.Equal(t, uint64(1), stats.failures.Load())
		})
	}
}
