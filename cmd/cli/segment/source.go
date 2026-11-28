package segment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

// ErrSourceConnection is returned when the s3 connection of a source cannot be told from the bootstrap file.
var ErrSourceConnection = errors.New("no s3 connection for the source")

// isObjectURL tells whether --from names an object of a bucket.
func isObjectURL(from string) bool { return strings.HasPrefix(from, "s3://") }

// sourceConnection picks the s3 connection of the source: the one that --source-connection names, or the only s3
// connection of the bootstrap file. Endpoint, region, credentials and TLS are the ones of the connection.
func sourceConnection(cfg *bootstrap.Config, name string) (string, *bootstrap.S3Connection, error) {
	if name != "" {
		conn, ok := cfg.Connections[name]
		if !ok || conn.S3 == nil {
			return "", nil, fmt.Errorf("--source-connection %q: %w: it is not an s3 connection of the bootstrap file",
				name, ErrSourceConnection)
		}

		return name, conn.S3, nil
	}

	var names []string

	for _, candidate := range slices.Sorted(maps.Keys(cfg.Connections)) {
		if cfg.Connections[candidate].S3 != nil {
			names = append(names, candidate)
		}
	}

	if len(names) != 1 {
		return "", nil, fmt.Errorf(
			"%w: the bootstrap file has %d s3 connections (%s): name one with --source-connection",
			ErrSourceConnection,
			len(names),
			strings.Join(names, ", "),
		)
	}

	return names[0], cfg.Connections[names[0]].S3, nil
}

// objects reads the bucket of the connection with a client from the one connection factory of the relay.
func (d *Deps) objects(ctx context.Context, spec *connection.S3Spec, name string) (*S3Objects, error) {
	if d.NewS3 != nil {
		client, err := d.NewS3(ctx, spec, name)
		if err != nil {
			return nil, err
		}

		return NewS3Objects(client, spec.Bucket), nil
	}

	factory := connection.New(d.ConnectionOptions...)

	err := factory.Set(name, spec)
	if err != nil {
		return nil, fmt.Errorf("connection %s: %w", name, err)
	}

	client, err := factory.S3(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("connection %s: %w", name, err)
	}

	return NewS3Objects(client, spec.Bucket), nil
}

// objectSource is the source that --from names, read through the s3 connection of the bootstrap file.
func objectSource(
	ctx context.Context, sess *session, from, srcConn string, maxObjects int, deps *Deps,
) (*segmentadmin.ObjectSource, error) {
	ref, err := segmentadmin.ParseObjectURL(from)
	if err != nil {
		return nil, fmt.Errorf("--from: %w", err)
	}

	name, conn, err := sourceConnection(sess.res.cfg, srcConn)
	if err != nil {
		return nil, err
	}

	if ref.Bucket != conn.Bucket {
		return nil, fmt.Errorf("--from: %w: the connection %q reads another bucket than the URL names",
			ErrSourceConnection, name)
	}

	spec, err := bootstrap.S3Spec(conn, sess.res.secrets)
	if err != nil {
		return nil, fmt.Errorf("connection %q: %w", name, err)
	}

	objects, err := deps.objects(ctx, spec, name)
	if err != nil {
		return nil, err
	}

	sess.log.LogAttrs(ctx, slog.LevelDebug, "source resolved", slog.String("connection", name),
		slog.Bool("prefix", ref.Prefix))

	return segmentadmin.NewObjectSource(objects, ref, maxObjects), nil
}
