//go:build docker

package segmentadmin_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmenttest"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

const lookupTimeout = 5 * time.Second

// backend is a store that the write side and the relay provider both reach, in the way that the relay does: a
// provider of the kind of the client, behind the resolver with the normalization of the segment.
type backend struct {
	store    segmentadmin.Store
	relay    segment.Provider
	provider *resolver.Resolver
	info     func(ctx context.Context, field string) (int64, error)
	// admin runs a command (ACL SETUSER ...) on every node of the store.
	admin func(ctx context.Context, args ...string) error
}

// credentials is the user that a backend connects as. Nil means the default user.
type credentials struct {
	user string
	pass string
}

type backendMaker struct {
	start func(tb testing.TB) *segmenttest.Server
	build func(tb testing.TB, srv *segmenttest.Server, segs map[string]string, as *credentials) *backend
	name  string
}

// providerSegments is the options of the segments in the provider of each client library. A key that ends with a
// colon is a prefix of member keys.
func providerSegments(
	keys map[string]string,
) (map[string]valkeysegment.SegmentOptions, map[string]redissegment.SegmentOptions) {
	valkeySegs := map[string]valkeysegment.SegmentOptions{}
	redisSegs := map[string]redissegment.SegmentOptions{}

	for name, key := range keys {
		layout := redissegment.LayoutSet
		if strings.HasSuffix(key, ":") {
			layout = redissegment.LayoutKeys
		}

		valkeySegs[name] = valkeysegment.SegmentOptions{Key: key, Layout: layout, VersionKey: versionKeyOf(key)}
		redisSegs[name] = redissegment.SegmentOptions{Key: key, Layout: layout, VersionKey: versionKeyOf(key)}
	}

	return valkeySegs, redisSegs
}

func wrap(tb testing.TB, inner segment.Provider) *resolver.Resolver {
	tb.Helper()

	res, err := resolver.New(inner, &resolver.Options{
		Default: resolver.SegmentOptions{Normalize: resolver.NormalizeTrimLower, NoCache: true, NoBreaker: true},
	})
	require.NoError(tb, err)

	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
		defer cancel()

		_ = res.Shutdown(ctx)
	})

	return res
}

func makers() []backendMaker {
	valkeyBuild := func(tb testing.TB, srv *segmenttest.Server, segs map[string]string, as *credentials) *backend {
		tb.Helper()

		opt := srv.ValkeyOption()
		if as != nil {
			opt.Username, opt.Password = as.user, as.pass
		}

		client, err := valkey.NewClient(opt)
		require.NoError(tb, err)

		valkeySegs, _ := providerSegments(segs)
		prov, err := valkeysegment.New(client, &valkeysegment.Options{Segments: valkeySegs, Timeout: lookupTimeout})
		require.NoError(tb, err)

		store := segmentadmin.NewValkey(client)
		tb.Cleanup(store.Close)

		return &backend{store: store, relay: prov, provider: wrap(tb, prov), info: valkeyInfo(client),
			admin: valkeyAdmin(client)}
	}

	redisBuild := func(tb testing.TB, srv *segmenttest.Server, segs map[string]string, as *credentials) *backend {
		tb.Helper()

		opts := srv.RedisOptions()
		if as != nil {
			opts.Username, opts.Password = as.user, as.pass
		}

		client := redis.NewUniversalClient(opts)
		_, redisSegs := providerSegments(segs)
		prov, err := redissegment.New(client, &redissegment.Options{Segments: redisSegs, Timeout: lookupTimeout})
		require.NoError(tb, err)

		store := segmentadmin.NewRedis(client)
		tb.Cleanup(store.Close)

		return &backend{store: store, relay: prov, provider: wrap(tb, prov), info: redisInfo(client),
			admin: redisAdmin(client)}
	}

	valkeyStart := func(tb testing.TB) *segmenttest.Server {
		tb.Helper()

		return segmenttest.StartServer(tb, segmenttest.ValkeyImage)
	}
	redisStart := func(tb testing.TB) *segmenttest.Server {
		tb.Helper()

		return segmenttest.StartServer(tb, segmenttest.RedisImage)
	}

	return []backendMaker{
		{name: "valkey-go on Valkey", start: valkeyStart, build: valkeyBuild},
		{name: "valkey-go on a Valkey cluster", start: segmenttest.StartCluster, build: valkeyBuild},
		{name: "go-redis on Valkey", start: valkeyStart, build: redisBuild},
		{name: "go-redis on a Valkey cluster", start: segmenttest.StartCluster, build: redisBuild},
		{name: "go-redis on Redis", start: redisStart, build: redisBuild},
	}
}

// valkeyAdmin runs a command on every node of the client.
func valkeyAdmin(client valkey.Client) func(context.Context, ...string) error {
	return func(ctx context.Context, args ...string) error {
		for _, node := range client.Nodes() {
			err := node.Do(ctx, node.B().Arbitrary(args...).Build()).Error()
			if err != nil {
				return fmt.Errorf("%s: %w", args[0], err)
			}
		}

		return nil
	}
}

// redisAdmin runs a command on every master of the client.
func redisAdmin(client redis.UniversalClient) func(context.Context, ...string) error {
	return func(ctx context.Context, args ...string) error {
		anyArgs := toAnyArgs(args)

		if cluster, ok := client.(*redis.ClusterClient); ok {
			err := cluster.ForEachMaster(ctx, func(ctx context.Context, node *redis.Client) error {
				return node.Do(ctx, anyArgs...).Err()
			})
			if err != nil {
				return fmt.Errorf("%s: %w", args[0], err)
			}

			return nil
		}

		err := client.Do(ctx, anyArgs...).Err()
		if err != nil {
			return fmt.Errorf("%s: %w", args[0], err)
		}

		return nil
	}
}

func toAnyArgs(args []string) []any {
	out := make([]any, len(args))
	for i, arg := range args {
		out[i] = arg
	}

	return out
}

// valkeyInfo reads a number of INFO memory (used_memory_peak, ...) of the node that serves the client.
func valkeyInfo(client valkey.Client) func(context.Context, string) (int64, error) {
	return func(ctx context.Context, field string) (int64, error) {
		text, err := client.Do(ctx, client.B().Info().Section("memory").Build()).ToString()
		if err != nil {
			return 0, fmt.Errorf("info: %w", err)
		}

		return infoField(text, field)
	}
}

func redisInfo(client redis.UniversalClient) func(context.Context, string) (int64, error) {
	return func(ctx context.Context, field string) (int64, error) {
		text, err := client.Info(ctx, "memory").Result()
		if err != nil {
			return 0, fmt.Errorf("info: %w", err)
		}

		return infoField(text, field)
	}
}

func infoField(text, field string) (int64, error) {
	prefix := field + ":"

	for _, line := range splitLines(text) {
		if len(line) > len(prefix) && line[:len(prefix)] == prefix {
			n, err := strconv.ParseInt(line[len(prefix):], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("info %s: %w", field, err)
			}

			return n, nil
		}
	}

	return 0, fmt.Errorf("%w: %s", errNoInfoField, field)
}

func splitLines(text string) []string {
	var out []string

	start := 0

	for i := range len(text) {
		if text[i] == '\n' {
			line := text[start:i]
			if n := len(line); n > 0 && line[n-1] == '\r' {
				line = line[:n-1]
			}

			out = append(out, line)
			start = i + 1
		}
	}

	return append(out, text[start:])
}

// idSource is an input of n ids, "<prefix>-<i>" one for each line, that calls onLine after each line it has produced.
type idSource struct {
	onLine func(line int)
	prefix string
	rest   []byte
	n      int
	i      int
	mu     sync.Mutex
	ended  bool
}

func (s *idSource) Read(buf []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	written := 0

	for written < len(buf) {
		if len(s.rest) == 0 {
			if s.i >= s.n {
				if !s.ended && s.onLine != nil {
					s.onLine(s.n + 1)
				}

				s.ended = true

				break
			}

			s.rest = []byte(s.prefix + "-" + strconv.Itoa(s.i) + "\n")
			s.i++

			if s.onLine != nil {
				s.onLine(s.i)
			}
		}

		copied := copy(buf[written:], s.rest)
		s.rest = s.rest[copied:]
		written += copied

		if s.onLine != nil && len(s.rest) == 0 {
			break // one line for each read, so that the hook of a line runs between the batches
		}
	}

	if written == 0 {
		return 0, io.EOF
	}

	return written, nil
}

func newReader(tb testing.TB, src io.Reader) *segmentadmin.Reader {
	tb.Helper()

	rd, err := segmentadmin.NewReader(src, segmentadmin.ReaderOptions{})
	require.NoError(tb, err)

	return rd
}

// states asks the relay provider about the ids of a segment in one call.
func states(ctx context.Context, prov segment.Provider, name string, ids ...string) ([]segment.State, error) {
	lookups := make([]segment.Lookup, len(ids))
	for i, id := range ids {
		lookups[i] = segment.Lookup{Segment: name, ID: id}
	}

	err := prov.Contains(ctx, lookups)
	out := make([]segment.State, len(ids))

	for i := range lookups {
		out[i] = lookups[i].State
	}

	if err != nil {
		return out, fmt.Errorf("contains: %w", err)
	}

	return out, nil
}

var errNoInfoField = errors.New("the INFO reply has no such field")

func stringsReader(text string) io.Reader { return strings.NewReader(text) }

// versionKeyOf is the version key of a segment key: outside the prefix of the member keys, which would count it.
func versionKeyOf(key string) string {
	if before, ok := strings.CutSuffix(key, ":"); ok {
		return before + "-version"
	}

	return key + "version"
}
