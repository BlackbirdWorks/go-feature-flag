package segmentstore

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
)

var errNoCertificate = errors.New("no certificate in the file")

// redisHandle is a go-redis client shared by every generation.
type redisHandle struct {
	client  redis.UniversalClient
	prefix  string
	timeout time.Duration
	atomic  bool
}

func redisDialer(conn *bootstrap.RedisConnection, secrets bootstrap.LoadOptions) Dialer {
	return func(context.Context) (Handle, error) {
		password, err := conn.Password.Resolve(secrets)
		if err != nil && conn.Password.IsSet() {
			return nil, fmt.Errorf("password: %w", err)
		}

		var tlsCfg *tls.Config

		if tlsEnabled(conn.TLS) {
			tlsCfg, err = tlsConfig(conn.TLS, secrets)
			if err != nil {
				return nil, err
			}
		}

		opts := &redis.UniversalOptions{
			Addrs: conn.Addresses, DB: conn.DB, Username: conn.Username, Password: password.Reveal(),
			TLSConfig: tlsCfg, IsClusterMode: conn.Cluster, ContextTimeoutEnabled: true,
		}
		if conn.MaxRetries != nil {
			opts.MaxRetries = *conn.MaxRetries
		}

		if conn.PoolSize != nil {
			opts.PoolSize = *conn.PoolSize
		}

		if t := conn.Timeouts; t != nil {
			opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout = durationOf(
				t.Connect,
			), durationOf(
				t.Read,
			), durationOf(
				t.Write,
			)
		}

		// go-redis connects on the first command: the dial itself cannot fail for a reachable configuration
		return &redisHandle{
			client: redis.NewUniversalClient(opts), prefix: conn.KeyPrefix, atomic: conn.Atomic,
			timeout: lookupTimeout(conn.LookupTimeout),
		}, nil
	}
}

func durationOf(d *bootstrap.Duration) time.Duration {
	if d == nil {
		return 0
	}

	return time.Duration(*d)
}

// Bind implements [Handle].
func (h *redisHandle) Bind(
	missing segment.MissingKey,
	defs []flagruntime.BoundDefinition,
) (*flagruntime.Binding, error) {
	segs := make(map[string]redissegment.SegmentOptions, len(defs))
	for _, def := range defs {
		segs[def.Name] = redissegment.SegmentOptions{
			Layout: redissegment.Layout(def.Layout), Key: def.Key, VersionKey: def.VersionKey,
		}
	}

	prov, err := redissegment.New(h.client, &redissegment.Options{
		Segments: segs, KeyPrefix: h.prefix, MissingKey: redissegment.MissingKey(missing), Atomic: h.atomic,
		Timeout: h.timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("redis provider: %w", err)
	}

	return &flagruntime.Binding{Provider: prov, Versioner: prov, Source: prov}, nil
}

// Close implements [Handle].
func (h *redisHandle) Close() error {
	err := h.client.Close()
	if err != nil {
		return fmt.Errorf("close the redis client: %w", err)
	}

	return nil
}

// lookupTimeout is the timeout of one lookup call: the setting of the connection, 100ms when unset.
func lookupTimeout(d *bootstrap.Duration) time.Duration {
	if d == nil {
		return bootstrap.DefaultLookupTimeout
	}

	return time.Duration(*d)
}
