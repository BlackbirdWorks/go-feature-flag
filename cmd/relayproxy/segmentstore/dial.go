package segmentstore

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

// dialerOf is the dialer of a connection by its kind; nil for a kind that cannot serve segments.
func dialerOf(conn *bootstrap.Connection, secrets bootstrap.LoadOptions) Dialer {
	switch {
	case conn.Redis != nil:
		return redisDialer(conn.Redis, secrets)
	case conn.Valkey != nil:
		return valkeyDialer(conn.Valkey, secrets)
	}

	return nil
}

// tlsEnabled reports whether the connection asks for TLS.
func tlsEnabled(cfg *bootstrap.StoreTLS) bool { return cfg != nil && cfg.Enable }

// tlsConfig is the TLS configuration of a connection that asks for TLS (see tlsEnabled).
func tlsConfig(cfg *bootstrap.StoreTLS, secrets bootstrap.LoadOptions) (*tls.Config, error) {
	out := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.ServerName}

	if cfg.CABundleFile != "" {
		read := secrets.ReadFile
		if read == nil {
			read = os.ReadFile
		}

		pem, err := read(cfg.CABundleFile)
		if err != nil {
			return nil, fmt.Errorf("tls.caBundleFile: %w", err)
		}

		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}

		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("tls.caBundleFile: %w", errNoCertificate)
		}

		out.RootCAs = pool
	}

	return out, nil
}

// errCertificate is the class of a dial that failed because the certificate of the store was not verified (a CA that
// did not sign it, a name that does not match, an expired certificate).
var errCertificate = errors.New("tls: the certificate of the store was not verified")

// dialClass reduces the error of a dial to its class, so that no address or certificate text reaches a log line.
func dialClass(err error) error {
	var (
		verify  *tls.CertificateVerificationError
		unknown x509.UnknownAuthorityError
		host    x509.HostnameError
		invalid x509.CertificateInvalidError
	)

	if errors.As(err, &verify) || errors.As(err, &unknown) || errors.As(err, &host) || errors.As(err, &invalid) {
		return errCertificate
	}

	return valkeysegment.ErrUnavailable
}
