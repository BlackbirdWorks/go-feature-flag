package segment

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/segmentstore"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

// writeTimeout is the write timeout of the valkey-go connection: a batch is bigger than a lookup.
const writeTimeout = 30 * time.Second

// Connector connects to the store of a connection of the bootstrap file.
type Connector func(context.Context, *bootstrap.Connection, bootstrap.LoadOptions) (segmentadmin.Store, error)

// Deps are the parts of the outside world that the commands use.
type Deps struct {
	// LookupEnv reads an environment variable (secrets and SEMAPHORE_* overrides).
	LookupEnv func(string) (string, bool)
	// ReadFile reads the bootstrap file, the segments document and file secrets.
	ReadFile func(string) ([]byte, error)
	// Stdin is the input of --from -.
	Stdin io.Reader
	// Open opens the input of --from.
	Open func(string) (io.ReadCloser, error)
	// Connect connects to a store.
	Connect Connector
	// NewS3 builds the client of an S3 source. Nil means the connection factory of the relay, which honours
	// ConnectionOptions.
	NewS3 func(ctx context.Context, spec *connection.S3Spec, name string) (S3Client, error)
	// ConnectionOptions are passed to the connection factory (a round tripper that counts requests, for example).
	ConnectionOptions []connection.Option
}

// DefaultDeps returns the dependencies of the real command.
func DefaultDeps() Deps {
	return Deps{
		LookupEnv: os.LookupEnv, ReadFile: os.ReadFile, Stdin: os.Stdin, Connect: connector(),
		Open: func(name string) (io.ReadCloser, error) { return os.Open(name) },
	}
}

// connector connects with the client library of the type of the connection.
func connector() Connector {
	return func(_ context.Context, conn *bootstrap.Connection, secrets bootstrap.LoadOptions) (segmentadmin.Store, error) {
		switch {
		case conn.Valkey != nil:
			opt, _, err := segmentstore.ValkeyClientOption(conn.Valkey, secrets)
			if err != nil {
				return nil, fmt.Errorf("valkey: %w", err)
			}

			opt.ConnWriteTimeout = max(opt.ConnWriteTimeout, writeTimeout)

			client, err := valkey.NewClient(opt)
			if err != nil {
				return nil, fmt.Errorf("valkey: %w", segmentadmin.ErrStore) // the error holds an address
			}

			return segmentadmin.NewValkey(client), nil
		case conn.Redis != nil:
			opts, err := segmentstore.RedisOptions(conn.Redis, secrets)
			if err != nil {
				return nil, fmt.Errorf("redis: %w", err)
			}

			return segmentadmin.NewRedis(redis.NewUniversalClient(opts)), nil
		}

		return nil, ErrNotStore
	}
}
