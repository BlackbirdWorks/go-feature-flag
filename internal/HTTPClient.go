package internal

import (
	"net/http"
	"time"
)

var _ HTTPClient = (*http.Client)(nil)

const defaultHTTPTimeout = 10 * time.Second

// HTTPClient is an interface over [http.Client] to make mock easier.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// DefaultHTTPClient returns an HTTP client with the default timeout.
func DefaultHTTPClient() *http.Client {
	return NewHTTPClient(defaultHTTPTimeout, nil)
}

// HTTPClientWithTimeout returns an HTTP client with the given timeout.
func HTTPClientWithTimeout(timeout time.Duration) *http.Client {
	return NewHTTPClient(timeout, nil)
}

// NewHTTPClient returns an HTTP client with the given timeout and transport.
func NewHTTPClient(timeout time.Duration, transport http.RoundTripper) *http.Client {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}
