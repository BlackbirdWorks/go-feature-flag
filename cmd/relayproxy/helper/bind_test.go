package helper_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/internal/protocol"
)

const textPlain = "text/plain"

// failingReader fails every read with err.
type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }

func TestBindBody_ReadFailure(t *testing.T) {
	t.Parallel()

	errTooLarge := helper.NewHTTPError(http.StatusRequestEntityTooLarge, "too large")

	tests := []struct {
		err         error
		name        string
		wantMessage string
		wantCode    int
	}{
		{name: "a body that can not be read is a 400", err: errBoom, wantCode: http.StatusBadRequest,
			wantMessage: "Bad Request"},
		{name: "a read error that has a status keeps it", err: errTooLarge,
			wantCode: http.StatusRequestEntityTooLarge, wantMessage: "too large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", http.NoBody)
			req.Body = io.NopCloser(failingReader{err: tt.err})
			req.ContentLength = 10
			req.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)

			var target struct{}

			err := helper.BindBody(req, &target)

			var httpErr *helper.HTTPError
			require.ErrorAs(t, err, &httpErr)
			require.ErrorIs(t, err, tt.err)
			assert.Equal(t, tt.wantCode, httpErr.Code)
			assert.Equal(t, tt.wantMessage, httpErr.Message)
		})
	}
}
