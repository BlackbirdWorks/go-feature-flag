package bootstrap

import (
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

// storeConnection checks a redis or valkey connection.
func (v *validator) storeConnection(path string, conn Connection) {
	var (
		addresses []string
		tlsCfg    *StoreTLS
		password  Secret
		prefix    string
	)

	switch {
	case conn.Redis != nil:
		r := conn.Redis
		addresses, tlsCfg, password, prefix = r.Addresses, r.TLS, r.Password, r.KeyPrefix

		v.redisSettings(path, r)
	case conn.Valkey != nil:
		k := conn.Valkey
		addresses, tlsCfg, password, prefix = k.Addresses, k.TLS, k.Password, k.KeyPrefix

		v.valkeySettings(path, k)
	}

	if len(addresses) == 0 {
		v.fail(path+".addresses", ErrMissingValue)
	}

	seen := map[string]int{}

	for i, address := range addresses {
		base := path + ".addresses[" + strconv.Itoa(i) + "]"

		if strings.ContainsFunc(address, unicode.IsSpace) {
			v.failf(base, ErrInvalidValue, "expected host:port without white space")
		} else {
			v.hostPort(base, address)
		}

		if first, dup := seen[address]; dup {
			v.failf(base, ErrDuplicateName, "same address as addresses[%d]", first)
		}

		seen[address] = i
	}

	v.secret(path+".password", password)
	v.storeTLS(path+".tls", tlsCfg)

	if strings.ContainsAny(prefix, " \t\r\n") {
		v.failf(path+".keyPrefix", ErrInvalidValue, "expected no white space")
	}
}

func (v *validator) redisSettings(path string, r *RedisConnection) {
	if r.DB < 0 {
		v.failf(path+".db", ErrInvalidValue, "expected a number from 0")
	}

	if r.PoolSize != nil && *r.PoolSize < 1 {
		v.failf(path+".poolSize", ErrInvalidValue, "expected an integer from 1")
	}

	if r.MaxRetries != nil && *r.MaxRetries < -1 {
		v.failf(path+".maxRetries", ErrInvalidValue, "expected an integer from -1 (-1 turns the retries off)")
	}

	if r.DB != 0 && (r.Cluster || len(r.Addresses) > 1) {
		v.failf(path+".db", ErrNotAllowed, "a cluster has no database number")
	}
}

func (v *validator) valkeySettings(path string, conn *ValkeyConnection) {
	v.oneOf(path+".pipelining", conn.Pipelining, PipeliningAlways, PipeliningAuto)

	if conn.BlockingPipeline < 0 {
		v.failf(path+".blockingPipeline", ErrInvalidValue, "expected an integer from 0")
	}

	if tm := conn.Timeouts; tm != nil && tm.IO != nil {
		if d := time.Duration(*tm.IO); d < valkeysegment.MinConnWriteTimeout || d > valkeysegment.MaxConnWriteTimeout {
			v.failf(path+".timeouts.io", ErrInvalidValue, "expected a duration from 10ms to 1m")
		} else if look := lookupTimeoutOf(conn.LookupTimeout); d < look {
			v.failf(path+".timeouts.io", ErrInvalidValue,
				"expected at least lookupTimeout (%s): a connection deadline below it cuts healthy replies", look)
		}
	}

	if conn.ForceSingleClient && len(conn.Addresses) > 1 {
		v.failf(path+".forceSingleClient", ErrNotAllowed, "one address only")
	}
}

func (v *validator) storeTLS(path string, t *StoreTLS) {
	if t == nil || t.Enable {
		return
	}

	if t.CABundleFile != "" {
		v.failf(path+".caBundleFile", ErrNotAllowed, "tls.enable is false")
	}

	if t.ServerName != "" {
		v.failf(path+".serverName", ErrNotAllowed, "tls.enable is false")
	}
}

func (v *validator) positiveInt(path string, n *int) {
	if n != nil && *n < 1 {
		v.failf(path, ErrInvalidValue, "expected an integer from 1")
	}
}

// segments checks the segments section.
func (v *validator) segments() {
	segs := v.cfg.Segments
	if segs == nil {
		return
	}

	res := segs.Resolver
	v.positiveInt("segments.resolver.maxOverdue", res.MaxOverdue)
	v.positiveInt("segments.resolver.maxIdBytes", res.MaxIDBytes)
	v.tuning("segments.resolver.default", &res.Default)
}

func (v *validator) tuning(path string, t *SegmentTuning) {
	if t.Normalize != "" {
		v.oneOf(path+".normalize", t.Normalize, NormalizeNone, NormalizeTrimLower)
	}

	v.positiveInt(path+".maxEntries", t.MaxEntries)
	v.positiveInt(path+".shards", t.Shards)
	v.positiveInt(path+".breakerFailures", t.BreakerFailures)
}

// lookupTimeoutOf is the lookup timeout of a connection with the default applied.
func lookupTimeoutOf(d *Duration) time.Duration {
	if d == nil {
		return DefaultLookupTimeout
	}

	return time.Duration(*d)
}
