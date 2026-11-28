//go:build docker

package corehttp_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/controlplane"
)

// The certificates are valid for a wide fixed window: TLS verification reads the real time, and the test must not
// depend on it.
func tlsValidFrom() time.Time  { return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) }
func tlsValidUntil() time.Time { return time.Date(2120, 1, 1, 0, 0, 0, 0, time.UTC) }

// tlsAuthority is a certificate authority of one test.
type tlsAuthority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTLSAuthority(tb testing.TB, name string) *tlsAuthority {
	tb.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(tb, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: tlsValidFrom(), NotAfter: tlsValidUntil(), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(tb, err)

	cert, err := x509.ParseCertificate(der)
	require.NoError(tb, err)

	return &tlsAuthority{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue returns the PEM certificate and key of a server named for localhost and the loopback addresses.
func (a *tlsAuthority) issue(tb testing.TB) ([]byte, []byte) {
	tb.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(tb, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "valkey"},
		NotBefore: tlsValidFrom(), NotAfter: tlsValidUntil(),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, a.cert, &key.PublicKey, a.key)
	require.NoError(tb, err)

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(tb, err)

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// startTLSValkey runs a Valkey that only speaks TLS, and returns it with a client of the test that trusts the CA.
func startTLSValkey(tb testing.TB, authority *tlsAuthority) *valkeyStore {
	tb.Helper()

	certPEM, keyPEM := authority.issue(tb)

	req := testcontainers.ContainerRequest{
		Image:        valkeyImage,
		ExposedPorts: []string{"6379/tcp"},
		Files: []testcontainers.ContainerFile{
			{Reader: bytes.NewReader(certPEM), ContainerFilePath: "/tls/server.crt", FileMode: 0o644},
			{Reader: bytes.NewReader(keyPEM), ContainerFilePath: "/tls/server.key", FileMode: 0o644},
			{Reader: bytes.NewReader(authority.pem), ContainerFilePath: "/tls/ca.crt", FileMode: 0o644},
		},
		Cmd: []string{
			"valkey-server", "--port", "0", "--tls-port", "6379", "--tls-cert-file", "/tls/server.crt",
			"--tls-key-file", "/tls/server.key", "--tls-ca-cert-file", "/tls/ca.crt", "--tls-auth-clients", "no",
		},
		WaitingFor: wait.ForLog("Ready to accept connections"),
	}

	container, err := testcontainers.GenericContainer(tb.Context(), testcontainers.GenericContainerRequest{
		ContainerRequest: req, Started: true,
	})
	testcontainers.CleanupContainer(tb, container)
	require.NoError(tb, err)

	host, err := container.Host(tb.Context())
	require.NoError(tb, err)

	port, err := container.MappedPort(tb.Context(), "6379/tcp")
	require.NoError(tb, err)

	address := net.JoinHostPort(host, port.Port())

	pool := x509.NewCertPool()
	require.True(tb, pool.AppendCertsFromPEM(authority.pem))

	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress: []string{address}, TLSConfig: tlsClientConfig(pool, host),
	})
	require.NoError(tb, err)
	tb.Cleanup(client.Close)

	return &valkeyStore{client: client, address: address}
}

// The connection options for TLS work against a real TLS-only store: the CA bundle file verifies it and the lookups are
// served. A CA that did not sign the certificate, and no TLS at all, are refused with a classified error that names
// neither the address nor the certificate, the segments answer STORE_ERROR, and the relay stays ready.
func TestE2E_SegmentStoreTLS(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{kindValkey, kindRedis} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			authority := newTLSAuthority(t, "test ca")
			store := startTLSValkey(t, authority)
			store.add(t, "aud", "alice")
			address := store.address

			dir := t.TempDir()
			goodCA, wrongCA := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "wrong.pem")
			require.NoError(t, os.WriteFile(goodCA, authority.pem, 0o600))
			require.NoError(t, os.WriteFile(wrongCA, newTLSAuthority(t, "other ca").pem, 0o600))

			tests := []struct {
				name       string
				tls        string
				wantClass  string
				wantMember bool
			}{
				{name: "the right CA", tls: ", tls: {enable: true, caBundleFile: " + goodCA + "}", wantMember: true},
				{
					name: "a CA that did not sign it", tls: ", tls: {enable: true, caBundleFile: " + wrongCA + "}",
					wantClass: "tls: the certificate of the store was not verified",
				},
				{name: "no TLS against a TLS store", tls: "", wantClass: "unavailable"},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()

					logs := &logBuffer{}

					conn := "  acl: {type: " + kind + ", addresses: [\"" + address + "\"], lookupTimeout: 300ms, " +
						"timeouts: {connect: 500ms}" + tt.tls
					if kind == kindValkey {
						conn += ", forceSingleClient: true"
					}

					rig := newV2With(t, &v2Options{
						connections: conn + "}\n", extra: resolverSection, cold: true,
						secrets: bootstrap.LoadOptions{ReadFile: os.ReadFile},
						logger:  slog.New(slog.NewJSONHandler(logs, nil)),
					})
					rig.flags[dynFlagsObject] = []byte(aclFlagsYAML)
					rig.segments = map[string][]byte{segObjectName: []byte(aclSegmentsYAML)}
					rig.sets = []controlplane.FlagSet{{
						Name: setName, Sources: []string{flagsObject, dynFlagsObject},
						SegmentSources: []string{segObjectName}, APIKeys: []string{tenantKey},
					}}

					rig.publish(t)
					rig.poll(t)

					awaitFlag(t, rig, "f-set", tt.wantMember, "the lookups over TLS")
					rig.wantProbe(t, "/readyz", http.StatusOK, bodyServing)

					if tt.wantMember {
						assert.Equal(t, "connected", infoOf(t, rig).SegmentStores[0].State)

						return
					}

					// valkey dials when the connection is opened; go-redis dials with the first call
					wantLine := map[string]string{kindValkey: "is not reachable", kindRedis: "a segment store fails"}[kind]

					require.Eventually(t, func() bool { return strings.Contains(logs.String(), wantLine) },
						eventually, poll, "the failure is logged once the dial fails")

					out := logs.String()
					assert.NotContains(t, out, address, "the address is never logged with the error")
					assert.NotContains(t, out, "certificate signed by unknown authority", "no raw TLS error text")

					if kind == kindValkey {
						assert.Contains(t, out, tt.wantClass, "the dial error is classified")
					}
				})
			}
		})
	}
}

func tlsClientConfig(pool *x509.CertPool, host string) *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: host}
}
