package events

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"hash/fnv"
	"maps"
	"slices"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/internal/connection"
)

// Specs are the sections of the bootstrap file the package reads.
type Specs struct {
	Connections map[string]bootstrap.Connection
	Exporters   map[string]bootstrap.Exporter
	Notifiers   map[string]bootstrap.Notifier
}

// SpecsFrom takes the sections of a bootstrap configuration.
func SpecsFrom(cfg *bootstrap.Config) Specs {
	return Specs{Connections: cfg.Connections, Exporters: cfg.Exporters, Notifiers: cfg.Notifiers}
}

// Builder builds exporters and notifiers from [Specs].
type Builder struct {
	// Factory gives the AWS clients and configurations of the connections. It is required for the AWS kinds.
	Factory *connection.Factory
	// Secrets reads the secrets of the entries (environment and files).
	Secrets bootstrap.LoadOptions
}

// identity is the running hash that gives the identity of an entry: the spec, the resolved secrets and the
// build count of the connection. Two builds with the same identity are interchangeable.
type identity struct {
	sum hash.Hash
	// secrets are the resolved secret values, to hide them from the errors of the exporter.
	secrets []string
}

func newIdentity(kind string, spec any) (*identity, error) {
	id := &identity{sum: fnv.New128a()}

	_, _ = id.sum.Write([]byte(kind + "\n"))

	// the secrets of the spec marshal as their reference or as the redaction marker, never as their value.
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("%w: identity: %w", ErrInvalidSettings, err)
	}

	_, _ = id.sum.Write(raw)

	return id, nil
}

func (id *identity) String() string { return hex.EncodeToString(id.sum.Sum(nil)) }

// add mixes a labelled value in the identity.
func (id *identity) add(label, value string) {
	_, _ = id.sum.Write([]byte("\n" + label + "=" + strconv.Itoa(len(value)) + ":" + value))
}

// secret resolves s, adds its value to the identity and returns it. An unset secret is the empty string.
func (b Builder) secret(id *identity, label string, s bootstrap.Secret) (string, error) {
	if !s.IsSet() {
		return "", nil
	}

	resolved, err := s.Resolve(b.Secrets)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", ErrUnresolvedSecret, label, err)
	}

	value := resolved.Reveal()
	id.add(label, value)

	if value != "" {
		id.secrets = append(id.secrets, value)
	}

	return value, nil
}

// headers resolves a header map, one value per name, in the order of the names.
func (b Builder) headers(id *identity, headers map[string]bootstrap.Secret) (map[string][]string, error) {
	if len(headers) == 0 {
		return nil, nil //nolint:nilnil // no header is not an error
	}

	out := make(map[string][]string, len(headers))

	for _, name := range slices.Sorted(maps.Keys(headers)) {
		value, err := b.secret(id, "headers."+name, headers[name])
		if err != nil {
			return nil, err
		}

		out[name] = []string{value}
	}

	return out, nil
}

// connectionOf returns the connection an entry names, checked for its type.
func connectionOf(conns map[string]bootstrap.Connection, name, wantType string) (bootstrap.Connection, error) {
	if name == "" {
		return bootstrap.Connection{}, settingsError("connection", "is required")
	}

	conn, ok := conns[name]
	if !ok {
		return bootstrap.Connection{}, fmt.Errorf("%w: %q", ErrUnknownConnection, name)
	}

	if conn.Type() != wantType {
		return bootstrap.Connection{}, fmt.Errorf("%w: connection %q is %q, want %q",
			ErrWrongConnectionKind, name, conn.Type(), wantType)
	}

	return conn, nil
}

// connectionIdentity mixes the connection in the identity: its settings and the number of times the factory
// built its configuration, which grows when the connection (credentials included) changes.
func (b Builder) connectionIdentity(id *identity, name string, conn bootstrap.Connection) error {
	raw, err := json.Marshal(conn)
	if err != nil {
		return fmt.Errorf("%w: identity: %w", ErrInvalidSettings, err)
	}

	id.add("connection."+name, string(raw))

	if b.Factory != nil {
		id.add("connection.builds", strconv.Itoa(b.Factory.Builds()[name]))
	}

	return nil
}

// awsConfig returns the AWS configuration of the connection from the factory.
func (b Builder) awsConfig(ctx context.Context, name string) (aws.Config, error) {
	if b.Factory == nil {
		return aws.Config{}, fmt.Errorf("%w: no connection factory", ErrConnectionClient)
	}

	cfg, err := b.Factory.AWSConfig(ctx, name)
	if err != nil {
		return aws.Config{}, fmt.Errorf("%w: %q: %w", ErrConnectionClient, name, err)
	}

	return cfg, nil
}

// s3Client returns the S3 client of the connection from the factory.
func (b Builder) s3Client(ctx context.Context, name string) (*s3.Client, error) {
	if b.Factory == nil {
		return nil, fmt.Errorf("%w: no connection factory", ErrConnectionClient)
	}

	client, err := b.Factory.S3(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrConnectionClient, name, err)
	}

	return client, nil
}
