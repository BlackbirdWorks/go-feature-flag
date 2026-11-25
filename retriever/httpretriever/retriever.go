package httpretriever

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/blackbirdworks/semaphore/internal"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/shared"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

var (
	errRequestTo                  = errors.New("request to")
	errHTTPDefaultTransportIs     = errors.New("http default transport is")
	errClientCertificateAndClient = errors.New("client certificate and client key must be provided together")
	errFailedToParseCA            = errors.New("failed to parse CA certificate")
)

// Retriever is a configuration struct for an HTTP endpoint retriever.
type Retriever struct {
	httpClient internal.HTTPClient
	// baseTransport is the transport the TLS transport is cloned from (http.DefaultTransport when nil).
	baseTransport http.RoundTripper
	// Header added to the request
	Header http.Header
	// URL of your endpoint
	URL string
	// HTTP Method we should use (default: GET)
	Method string
	// Body of the request if needed (default: empty body)
	Body string
	// ClientCertPath is the path to the client certificate file used for mTLS.
	ClientCertPath string
	// ClientKeyPath is the path to the client certificate key file used for mTLS.
	ClientKeyPath string
	// CACertPath is the path to the CA certificate file used to verify the server certificate.
	CACertPath string
	status     retriever.Status
	// Timeout we should wait before failing (default: 10 seconds)
	Timeout time.Duration
}

// SetBaseTransport sets the transport that the TLS transport (mTLS, custom CA) is cloned from, which
// must be a *[http.Transport]. It is [http.DefaultTransport] by default.
func (r *Retriever) SetBaseTransport(transport http.RoundTripper) {
	r.baseTransport = transport
}

// SetHTTPClient is here if you want to override the default [http.Client] we are using.
// It is also used for the tests.
func (r *Retriever) SetHTTPClient(client internal.HTTPClient) {
	r.httpClient = client
}

// Init is initializing the retriever to start fetching the flags configuration.
func (r *Retriever) Init(_ context.Context, _ *fflog.FFLogger) error {
	r.status = retriever.RetrieverNotReady

	_, err := r.getHTTPClient()
	if err != nil {
		r.status = retriever.RetrieverError

		return err
	}

	r.status = retriever.RetrieverReady

	return nil
}

// Shutdown gracefully shutdown the retriever and set the status as not ready.
func (r *Retriever) Shutdown(_ context.Context) error {
	r.httpClient = nil
	r.status = retriever.RetrieverNotReady

	return nil
}

// Status is the function returning the internal state of the retriever.
func (r *Retriever) Status() retriever.Status {
	if r == nil || r.status == "" {
		return retriever.RetrieverNotReady
	}

	return r.status
}

// Retrieve is the function in charge of fetching the flag configuration.
func (r *Retriever) Retrieve(ctx context.Context) ([]byte, error) {
	httpClient, err := r.getHTTPClient()
	if err != nil {
		return nil, err
	}

	resp, err := shared.CallHTTPAPI(ctx, r.URL, r.Method, r.Body, r.Timeout, r.Header, httpClient)
	if err != nil {
		return nil, fmt.Errorf("fetch HTTP flag file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("%w %s failed with code %d", errRequestTo, internal.RedactURL(r.URL), resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read HTTP response body: %w", err)
	}

	return body, nil
}

func (r *Retriever) getHTTPClient() (internal.HTTPClient, error) {
	if r.httpClient != nil {
		return r.httpClient, nil
	}

	var transport http.RoundTripper

	if r.ClientCertPath != "" || r.ClientKeyPath != "" || r.CACertPath != "" {
		tlsConfig, err := r.tlsConfig()
		if err != nil {
			return nil, err
		}

		base := r.baseTransport
		if base == nil {
			base = http.DefaultTransport
		}

		defaultTransport, ok := base.(*http.Transport)
		if !ok {
			return nil, fmt.Errorf("%w %T, expected *http.Transport", errHTTPDefaultTransportIs, base)
		}

		tlsTransport := defaultTransport.Clone()
		tlsTransport.TLSClientConfig = tlsConfig
		transport = tlsTransport
	}

	r.httpClient = internal.NewHTTPClient(r.Timeout, transport)

	return r.httpClient, nil
}

func (r *Retriever) tlsConfig() (*tls.Config, error) {
	config := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	certificates, err := r.clientCertificates()
	if err != nil {
		return nil, err
	}

	config.Certificates = certificates

	rootCAs, err := r.rootCAs()
	if err != nil {
		return nil, err
	}

	config.RootCAs = rootCAs

	return config, nil
}

func (r *Retriever) clientCertificates() ([]tls.Certificate, error) {
	if (r.ClientCertPath == "") != (r.ClientKeyPath == "") {
		return nil, errClientCertificateAndClient
	}

	if r.ClientCertPath == "" {
		return nil, nil
	}

	cert, err := tls.LoadX509KeyPair(r.ClientCertPath, r.ClientKeyPath)
	if err != nil {
		return nil, fmt.Errorf("loading the client key pair: %w", err)
	}

	return []tls.Certificate{cert}, nil
}

func (r *Retriever) rootCAs() (*x509.CertPool, error) {
	if r.CACertPath == "" {
		//nolint:nilnil // a nil pool means no custom CA is configured, the transport then uses the system pool
		return nil, nil
	}

	rootCAs, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("loading the system certificate pool: %w", err)
	}

	if rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}

	caCert, err := os.ReadFile(r.CACertPath)
	if err != nil {
		return nil, fmt.Errorf("reading the CA certificate: %w", err)
	}

	if !rootCAs.AppendCertsFromPEM(caCert) {
		return nil, errFailedToParseCA
	}

	return rootCAs, nil
}
