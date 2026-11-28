package connection_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	certValidBackdate = time.Hour
	certValidForward  = 24 * time.Hour
	darwinUntrusted   = "certificate is not trusted"
)

// certValidity returns a short window around the current wall clock. Production TLS verification always checks
// certificates against the real clock, and macOS Security.framework rejects TLS server certificates issued on
// or after 2019-07-01 whose validity exceeds 825 days as not standards compliant before checking trust.
func certValidity() (time.Time, time.Time) {
	now := time.Now().UTC()

	return now.Add(-certValidBackdate), now.Add(certValidForward)
}

// testCA is a certificate authority created for one test, and the servers it can sign for.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	// pem is the certificate of the authority, the content of a bundle file.
	pem []byte
	// serial is the last serial number issued.
	serial int64
}

// newTestCA creates a CA. Two calls give two CAs that do not trust each other.
func newTestCA(t *testing.T, name string) *testCA {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	notBefore, notAfter := certValidity()

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	return &testCA{
		cert: cert, key: key, serial: 1,
		pem: pem.EncodeToMemory(&pem.Block{Type: pemCertificate, Bytes: der}),
	}
}

// countingServer is a TLS test server that counts the requests it completed and the TLS handshakes it refused.
type countingServer struct {
	*httptest.Server

	requests atomic.Int32
	refused  atomic.Int32
}

// refusalCounter is a slog handler that counts the TLS handshake errors the HTTP server logs.
type refusalCounter struct{ refused *atomic.Int32 }

func (refusalCounter) Enabled(context.Context, slog.Level) bool { return true }

//nolint:gocritic // slog.Handler requires the Record by value
func (r refusalCounter) Handle(_ context.Context, record slog.Record) error {
	if strings.Contains(record.Message, "TLS handshake error") {
		r.refused.Add(1)
	}

	return nil
}
func (r refusalCounter) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r refusalCounter) WithGroup(string) slog.Handler      { return r }

// startCounting starts a TLS server issued by the CA that counts completed requests and refused handshakes.
func (ca *testCA) startCounting(t *testing.T, handler http.Handler) *countingServer {
	t.Helper()

	counted := &countingServer{}
	counted.Server = ca.serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counted.requests.Add(1)
		handler.ServeHTTP(w, r)
	}), func(srv *http.Server) {
		srv.ErrorLog = slog.NewLogLogger(refusalCounter{refused: &counted.refused}, slog.LevelError)
	})

	return counted
}

// startServer starts a TLS server on 127.0.0.1 whose certificate is issued by the CA.
func (ca *testCA) startServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	return ca.serve(t, handler, nil)
}

func (ca *testCA) serve(t *testing.T, handler http.Handler, configure func(*http.Server)) *httptest.Server {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	ca.serial++

	notBefore, notAfter := certValidity()

	template := &x509.Certificate{
		SerialNumber: big.NewInt(ca.serial),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)

	srv := httptest.NewUnstartedServer(handler)
	if configure != nil {
		configure(srv.Config)
	}

	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	return srv
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

// requireUntrustedCertError asserts that err failed TLS certificate verification because the server's
// authority is not trusted. Custom CA bundles and non-Darwin platforms return [x509.UnknownAuthorityError];
// macOS Security.framework reports system-roots untrusted-authority errors as "certificate is not trusted"
// rather than mapping them to [x509.UnknownAuthorityError].
func requireUntrustedCertError(t *testing.T, err error) {
	t.Helper()

	var verifyErr *tls.CertificateVerificationError

	require.ErrorAs(t, err, &verifyErr)

	if _, ok := errors.AsType[x509.UnknownAuthorityError](verifyErr.Err); ok {
		return
	}

	if runtime.GOOS == "darwin" {
		require.ErrorContains(t, verifyErr.Err, darwinUntrusted)

		return
	}

	var unknownAuth x509.UnknownAuthorityError

	require.ErrorAs(t, verifyErr.Err, &unknownAuth)
}
