package internal_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal"
)

func TestRedactURLError(t *testing.T) {
	t.Parallel()
	const secretURL = "https://user:SECRETMARKER-pass@hooks.example.com/services/SECRETMARKER-token?key=SECRETMARKER-q"

	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()

	_, parseErr := url.Parse("https://{}hooks.example.com/SECRETMARKER-token")
	require.Error(t, parseErr)
	_, doErr := http.Get(closedURL + "/SECRETMARKER-token") //nolint:noctx // test of the error message only
	require.Error(t, doErr)

	tests := []struct {
		name        string
		err         error
		wantMessage string
		wantSame    bool
	}{
		{name: "url.Parse error", err: parseErr, wantMessage: `parse "[REDACTED]": invalid URL`},
		{name: "http client error", err: doErr, wantMessage: `Get "[REDACTED]": dial tcp`},
		{
			name: "url.Error from a custom client", wantMessage: `Post "[REDACTED]": boom`,
			err: &url.Error{Op: "Post", URL: secretURL, Err: errors.New("boom")},
		},
		{
			name: "wrapped url.Error", wantMessage: `Get "[REDACTED]": EOF`,
			err: fmt.Errorf("wrapped: %w", &url.Error{Op: "Get", URL: secretURL, Err: io.EOF}),
		},
		{
			name: "malformed redirect location", wantMessage: `Get "[REDACTED]": invalid Location header`,
			err: &url.Error{Op: "Get", URL: secretURL, Err: fmt.Errorf(
				"failed to parse Location header %q: invalid character", "https://u:SECRETMARKER-p@bad host/x")},
		},
		{
			name: "cause quoting an url", wantMessage: `Get "[REDACTED]": bad [REDACTED] here`,
			err: &url.Error{Op: "Get", URL: secretURL, Err: errors.New(`bad https://u:SECRETMARKER-p@h/p?t=SECRETMARKER-q here`)},
		},
		{name: "plain error is untouched", err: errors.New("random error"), wantMessage: "random error", wantSame: true},
		{name: "nil", err: nil, wantSame: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := internal.RedactURLError(tt.err)
			if tt.err == nil {
				assert.NoError(t, got)
				return
			}
			assert.NotContains(t, got.Error(), "SECRETMARKER")
			assert.Contains(t, got.Error(), tt.wantMessage)
			if tt.wantSame {
				assert.Equal(t, tt.err, got)
			}
			if urlErr, ok := errors.AsType[*url.Error](tt.err); ok {
				assert.ErrorAs(t, got, &urlErr, "the error stays a *url.Error")
			}
		})
	}
}

func TestRedactURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "userinfo and query dropped", raw: "https://user:SECRETMARKER@flags.example.com/SECRETMARKER/b.yaml?token=SECRETMARKER#frag", want: "https://flags.example.com"},
		{name: "scheme and host kept", raw: "http://localhost:8080/flags", want: "http://localhost:8080"},
		{name: "unparsable value replaced", raw: "http://us:SECRETMARKER@host:bad/x", want: internal.RedactedURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := internal.RedactURL(tt.raw)
			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, "SECRETMARKER")
		})
	}
}

func TestRedactURLError_UnparsableURLs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{name: "password parsed as invalid port", raw: "https://user:SECRETMARKER"},
		{name: "invalid port with path", raw: "https://h:99999SECRETMARKER/p"},
		{name: "invalid escape in path", raw: "https://u:p@h/%zzSECRETMARKER"},
		{name: "invalid escape in user info", raw: "https://u%zzSECRETMARKER:p@h"},
		{name: "control character", raw: "https://h/\x7fSECRETMARKER"},
		{name: "missing scheme", raw: "://SECRETMARKER"},
		{name: "colon in first path segment", raw: "1http://SECRETMARKER"},
		{name: "unclosed ipv6 host", raw: "https://[::1SECRETMARKER/"},
		{name: "invalid host character", raw: "https://h{}SECRETMARKER"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, parseErr := url.Parse(tt.raw)
			require.Error(t, parseErr)
			require.Contains(t, parseErr.Error(), "SECRETMARKER", "the shape must make url.Parse quote the input")

			_, reqErr := http.NewRequest(http.MethodGet, tt.raw, nil) //nolint:noctx // error message only
			require.Error(t, reqErr)

			for _, in := range []error{parseErr, reqErr, fmt.Errorf("wrapped: %w", parseErr)} {
				got := internal.RedactURLError(in)
				require.Error(t, got)
				assert.NotContains(t, got.Error(), "SECRETMARKER")
				assert.NotContains(t, fmt.Sprintf("%v %+v %#v", got, got, got), "SECRETMARKER")
				assert.Contains(t, got.Error(), internal.RedactedURL)
			}
		})
	}
}
