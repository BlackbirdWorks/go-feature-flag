package testutils

import (
	"bytes"
	"errors"
	"io"
	"net/http"
)

var (
	errRandomError = errors.New("random error")
)

type HTTPClientMock struct {
	ForceError bool
	StatusCode int
	Body       string
	Signature  string
	Headers    map[string][]string
}

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
