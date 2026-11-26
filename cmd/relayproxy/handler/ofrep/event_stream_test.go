package ofrep_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
)

func TestNewEventStream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr     error
		name        string
		baseURL     string
		wantMessage string
		delay       int
		wantEnabled bool
	}{
		{name: "no base URL advertises nothing"},
		{name: "no base URL with a delay advertises nothing", delay: 30},
		{name: "https URL", baseURL: "https://example.com", delay: 60, wantEnabled: true},
		{name: "http URL with a path prefix", baseURL: "http://example.com/goff/", wantEnabled: true},
		{name: "URL with a port", baseURL: "https://example.com:8443", wantEnabled: true},
		{name: "negative delay", baseURL: "https://example.com", delay: -1,
			wantErr: ofrep.ErrEventStreamInactivityDelay, wantMessage: "got -1"},
		{name: "negative delay without base URL", delay: -5, wantErr: ofrep.ErrEventStreamInactivityDelay},
		{name: "not a URL", baseURL: "://nope", wantErr: ofrep.ErrEventStreamBaseURL},
		{name: "relative URL", baseURL: "/stream", wantErr: ofrep.ErrEventStreamBaseURL},
		{name: "scheme that is not http", baseURL: "ftp://example.com", wantErr: ofrep.ErrEventStreamBaseURL},
		{name: "no host", baseURL: "https://", wantErr: ofrep.ErrEventStreamBaseURL},
		{name: "query", baseURL: "https://example.com?a=b", wantErr: ofrep.ErrEventStreamBaseURLQuery},
		{name: "fragment", baseURL: "https://example.com#top", wantErr: ofrep.ErrEventStreamBaseURLQuery},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ofrep.NewEventStream(tt.baseURL, tt.delay)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Equal(t, ofrep.EventStream{}, got, "an invalid value is never usable")
				assert.False(t, got.IsEnabled())

				if tt.wantMessage != "" {
					require.ErrorContains(t, err, tt.wantMessage)
				}

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantEnabled, got.IsEnabled())
		})
	}
}

// TestNewEventStream_ErrorHidesCredentials checks that a base URL with user info never reaches the error.
func TestNewEventStream_ErrorHidesCredentials(t *testing.T) {
	t.Parallel()

	_, err := ofrep.NewEventStream("https://user:secret@exa mple.com", 0)

	require.ErrorIs(t, err, ofrep.ErrEventStreamBaseURL)
	assert.NotContains(t, err.Error(), "secret")
}
