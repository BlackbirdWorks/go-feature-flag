package connection_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The certificates are valid from 2020 to 2120. TLS verification reads the real time, and the tests must not
// depend on it (nor inject a clock into the production TLS config), so the window is a wide fixed one that
// stays valid for decades.
func certValidFrom() time.Time  { return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) }
func certValidUntil() time.Time { return time.Date(2120, 1, 1, 0, 0, 0, 0, time.UTC) }

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

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             certValidFrom(),
		NotAfter:              certValidUntil(),
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

// startServer starts a TLS server on 127.0.0.1 whose certificate is issued by the CA.
func (ca *testCA) startServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	ca.serial++

	template := &x509.Certificate{
		SerialNumber: big.NewInt(ca.serial),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    certValidFrom(),
		NotAfter:     certValidUntil(),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)

	srv := httptest.NewUnstartedServer(handler)
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
