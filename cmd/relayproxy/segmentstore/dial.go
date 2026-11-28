package segmentstore

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
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
