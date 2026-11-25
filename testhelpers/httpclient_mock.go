package testhelpers

import (
	"bytes"
	"errors"
	"io"
	"net/http"
)

var (
	errRandomError = errors.New("random error")
)

// HTTPClientMock is a mock HTTP client used in tests.
type HTTPClientMock struct {
	Headers    map[string][]string
	Body       string
	Signature  string
	StatusCode int
	ForceError bool
}

// Do returns the configured response for the request.
func (h *HTTPClientMock) Do(req *http.Request) (*http.Response, error) {
	if h.ForceError {
		return nil, errRandomError
	}

	b, _ := io.ReadAll(req.Body)
	h.Body = string(b)
	h.Signature = req.Header.Get("X-Hub-Signature-256")
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewReader([]byte(""))),
	}
	h.Headers = req.Header.Clone()
	resp.StatusCode = h.StatusCode

	return resp, nil
}
