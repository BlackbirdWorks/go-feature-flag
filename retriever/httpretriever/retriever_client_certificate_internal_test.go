package httpretriever

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io/fs"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	retrieverpkg "github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/testutils/mock"
)

func TestRetriever_getHTTPClient_WithClientCertificate(t *testing.T) {
	t.Parallel()

	certFile, keyFile := createClientCertificateFiles(t)

	retriever := Retriever{
		ClientCertPath: certFile,
		ClientKeyPath:  keyFile,
		Timeout:        2 * time.Second,
	}

	client, err := retriever.getHTTPClient()
	require.NoError(t, err)

	httpClient, ok := client.(*http.Client)
	require.True(t, ok)
	assert.Equal(t, 2*time.Second, httpClient.Timeout)

	transport, ok := httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport.TLSClientConfig)
	assert.Len(t, transport.TLSClientConfig.Certificates, 1)
	assert.Equal(t, uint16(tls.VersionTLS12), transport.TLSClientConfig.MinVersion)
}

func TestRetriever_getHTTPClient_WithCACertificate(t *testing.T) {
	t.Parallel()

	caCertFile, _ := createClientCertificateFiles(t)

	retriever := Retriever{
		CACertPath: caCertFile,
	}

	client, err := retriever.getHTTPClient()
	require.NoError(t, err)

	httpClient, ok := client.(*http.Client)
	require.True(t, ok)
	assert.Equal(t, 10*time.Second, httpClient.Timeout)

	transport, ok := httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport.TLSClientConfig)
	require.NotNil(t, transport.TLSClientConfig.RootCAs)
	assert.Equal(t, uint16(tls.VersionTLS12), transport.TLSClientConfig.MinVersion)
}

func TestRetriever_getHTTPClient_WithUnsupportedDefaultTransport(t *testing.T) {
	t.Parallel()

	caCertFile, _ := createClientCertificateFiles(t)

	retriever := Retriever{
		CACertPath:    caCertFile,
		baseTransport: unsupportedTransport{},
	}

	err := retriever.Init(context.Background(), nil)
	require.Error(t, err)
	assert.Equal(
		t,
		"http default transport is httpretriever.unsupportedTransport, expected *http.Transport",
		err.Error(),
	)
	assert.Equal(t, retrieverpkg.RetrieverError, retriever.Status())
}

func TestRetriever_getHTTPClient_WithoutClientCertificateConfiguration(t *testing.T) {
	t.Parallel()

	retriever := Retriever{
		Timeout: 2 * time.Second,
	}

	client, err := retriever.getHTTPClient()
	require.NoError(t, err)

	httpClient, ok := client.(*http.Client)
	require.True(t, ok)
	assert.Equal(t, 2*time.Second, httpClient.Timeout)
}

func TestRetriever_getHTTPClient_CacheGeneratedHTTPClient(t *testing.T) {
	t.Parallel()

	caCertFile, _ := createClientCertificateFiles(t)
	retriever := Retriever{
		CACertPath: caCertFile,
	}

	client, err := retriever.getHTTPClient()
	require.NoError(t, err)

	cachedClient, err := retriever.getHTTPClient()
	require.NoError(t, err)
	assert.Same(t, client, cachedClient)
}

func TestRetriever_Init(t *testing.T) {
	t.Parallel()

	caCertFile, _ := createClientCertificateFiles(t)
	retriever := Retriever{
		CACertPath: caCertFile,
	}

	assert.Equal(t, retrieverpkg.RetrieverNotReady, retriever.Status())

	err := retriever.Init(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, retrieverpkg.RetrieverReady, retriever.Status())

	client, err := retriever.getHTTPClient()
	require.NoError(t, err)
	assert.Same(t, retriever.httpClient, client)

	err = retriever.Shutdown(context.Background())
	require.NoError(t, err)
	assert.Equal(t, retrieverpkg.RetrieverNotReady, retriever.Status())
	assert.Nil(t, retriever.httpClient)
}

func TestRetriever_getHTTPClient_WithInvalidClientCertificate(t *testing.T) {
	t.Parallel()

	certFile, keyFile := createClientCertificateFiles(t)
	require.NoError(t, os.WriteFile(keyFile, []byte("invalid-key"), 0o600))

	retriever := Retriever{
		ClientCertPath: certFile,
		ClientKeyPath:  keyFile,
	}

	_, err := retriever.getHTTPClient()
	require.EqualError(t, err, "tls: failed to find any PEM data in key input")
}

func TestRetriever_getHTTPClient_WithMissingCACertificate(t *testing.T) {
	t.Parallel()

	retriever := Retriever{
		CACertPath: filepath.Join(t.TempDir(), "missing-ca.crt"),
	}

	_, err := retriever.getHTTPClient()
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestRetriever_getHTTPClient_WithInvalidCACertificate(t *testing.T) {
	t.Parallel()

	caCertFile := filepath.Join(t.TempDir(), "invalid-ca.crt")
	require.NoError(t, os.WriteFile(caCertFile, []byte("invalid-ca"), 0o600))

	retriever := Retriever{
		CACertPath: caCertFile,
	}

	_, err := retriever.getHTTPClient()
	require.Error(t, err)
	assert.Equal(t, "failed to parse CA certificate", err.Error())
}

func TestRetriever_Retrieve_WithIncompleteClientCertificateConfiguration(t *testing.T) {
	t.Parallel()

	httpClient := mock.HTTP{}
	retriever := Retriever{
		URL:            "http://localhost.example/file",
		ClientCertPath: "client.crt",
	}
	retriever.SetHTTPClient(&httpClient)

	_, err := retriever.Retrieve(context.Background())
	require.NoError(t, err)
	assert.True(t, httpClient.HasBeenCalled)

	retriever.SetHTTPClient(nil)
	_, err = retriever.Retrieve(context.Background())
	require.Error(t, err)
	assert.Equal(t, "client certificate and client key must be provided together", err.Error())
}

type unsupportedTransport struct{}

func (unsupportedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, nil //nolint:nilnil // the test feeds the client a nil response on purpose
}

func createClientCertificateFiles(t *testing.T) (string, string) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "semaphore-test-client",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)

	tmpDir := t.TempDir()
	certFile := filepath.Join(tmpDir, "client.crt")
	keyFile := filepath.Join(tmpDir, "client.key")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	require.NoError(t, os.WriteFile(certFile, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0o600))

	return certFile, keyFile
}

func TestRetriever_SetBaseTransport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		transport http.RoundTripper
		wantErr   string
	}{
		{
			name:      "a transport that is not a *http.Transport is refused",
			transport: unsupportedTransport{},
			wantErr:   "http default transport is httpretriever.unsupportedTransport, expected *http.Transport",
		},
		{name: "a *http.Transport is cloned", transport: &http.Transport{}, wantErr: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			caCertFile, _ := createClientCertificateFiles(t)
			r := Retriever{CACertPath: caCertFile}
			r.SetBaseTransport(tt.transport)

			err := r.Init(context.Background(), nil)

			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
