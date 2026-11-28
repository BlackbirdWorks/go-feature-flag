package segmentstore

import (
	"context"
	"crypto/tls"
	"fmt"
	"math"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

// valkeyHandle is a valkey-go client shared by every generation.
type valkeyHandle struct {
	client    valkey.Client
	prefix    string
	timeout   time.Duration
	nonAtomic bool
}

func valkeyDialer(conn *bootstrap.ValkeyConnection, secrets bootstrap.LoadOptions) Dialer {
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

		opt := valkey.ClientOption{
			InitAddress: conn.Addresses, Username: conn.Username, Password: password.Reveal(), TLSConfig: tlsCfg,
			ForceSingleClient: conn.ForceSingleClient,
			// the lookups go through DoMulti, which is never cached: the resolver caches
			DisableCache:     true,
			DisableRetry:     !conn.Retry,
			AlwaysPipelining: conn.Pipelining == "" || conn.Pipelining == bootstrap.PipeliningAlways,
			BlockingPipeline: conn.BlockingPipeline,
		}
		if opt.BlockingPipeline == 0 {
			opt.BlockingPipeline = math.MaxInt // a batch of any size stays on the shared connection
		}

		timeout := lookupTimeout(conn.LookupTimeout)
		opt.ConnWriteTimeout = valkeysegment.DefaultConnWriteTimeout(timeout)

		if t := conn.Timeouts; t != nil {
			opt.Dialer.Timeout = durationOf(t.Connect)

			if io := durationOf(t.IO); io > 0 {
				opt.ConnWriteTimeout = io
			}
		}

		client, err := valkey.NewClient(opt)
		if err != nil {
			return nil, fmt.Errorf("valkey: %w", valkeysegment.ErrUnavailable) // the error holds an address
		}

		return &valkeyHandle{
			client: client, prefix: conn.KeyPrefix, timeout: timeout,
			nonAtomic: conn.Atomic != nil && !*conn.Atomic,
		}, nil
	}
}

// Bind implements [Handle].
func (h *valkeyHandle) Bind(
	missing segment.MissingKey,
	defs []flagruntime.BoundDefinition,
) (*flagruntime.Binding, error) {
	segs := make(map[string]valkeysegment.SegmentOptions, len(defs))
	for _, def := range defs {
		segs[def.Name] = valkeysegment.SegmentOptions{
			Layout:     valkeysegment.Layout(def.Layout),
			Key:        def.Key,
			VersionKey: def.VersionKey,
		}
	}

	prov, err := valkeysegment.New(h.client, &valkeysegment.Options{
		Segments: segs, KeyPrefix: h.prefix, MissingKey: valkeysegment.MissingKey(missing), Timeout: h.timeout,
		DisableAtomic: h.nonAtomic,
	})
	if err != nil {
		return nil, fmt.Errorf("valkey provider: %w", err)
	}

	return &flagruntime.Binding{Provider: prov, Versioner: prov}, nil
}

// Close implements [Handle].
func (h *valkeyHandle) Close() error {
	h.client.Close()

	return nil
}
