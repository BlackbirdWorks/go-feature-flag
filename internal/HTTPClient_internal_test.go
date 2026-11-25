package internal

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHTTPClient(t *testing.T) {
	t.Parallel()

	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	require.True(t, ok)
	transport := defaultTransport.Clone()

	client := NewHTTPClient(2*time.Second, transport)

	assert.Equal(t, 2*time.Second, client.Timeout)
	assert.Same(t, transport, client.Transport)
}

func TestNewHTTPClient_WithDefaultTimeout(t *testing.T) {
	t.Parallel()

	client := NewHTTPClient(0, nil)

	assert.Equal(t, 10*time.Second, client.Timeout)
	assert.Nil(t, client.Transport)
}
