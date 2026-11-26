package publish

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
	"github.com/blackbirdworks/semaphore/internal/connection"
)

// Defaults of the explicit target flags.
const (
	defaultChannel    = "stable"
	defaultConnection = "control"
)

// Errors of the target resolution. Test them with [errors.Is].
var (
	// ErrUsage is returned for flags that cannot be combined or are missing.
	ErrUsage = errors.New("usage")
	// ErrNoControlPlane is returned when the bootstrap file has no controlPlane section.
	ErrNoControlPlane = errors.New("the bootstrap file has no controlPlane section")
	// ErrNotS3 is returned when the control-plane connection is not an S3 connection.
	ErrNotS3 = errors.New("the controlPlane connection is not an s3 connection")
)

// targetFlags are the flags that say where to publish.
type targetFlags struct {
	config      string
	bucket      string
	prefix      string
	region      string
	endpoint    string
	channel     string
	connection  string
	manifestKey string
}

// explicit lists the flags that only make sense without --config.
func (flags *targetFlags) explicit() []string {
	var set []string

	for _, p := range []struct{ name, value string }{
		{"--bucket", flags.bucket}, {"--region", flags.region}, {"--endpoint", flags.endpoint}, {"--channel", flags.channel},
		{"--connection", flags.connection}, {"--manifest-key", flags.manifestKey},
	} {
		if p.value != "" {
			set = append(set, p.name)
		}
	}

	return set
}

// target is where to publish and how to reach it.
type target struct {
	spec *connection.S3Spec
	name string
	cfg  s3publish.Config
}

// resolveTarget builds the target from the bootstrap file (--config) or from the explicit flags.
func resolveTarget(flags *targetFlags, deps *Deps) (*target, error) {
	if flags.config != "" {
		if set := flags.explicit(); len(set) > 0 {
			return nil, fmt.Errorf("%w: %s cannot be combined with --config (the file says where to publish)",
				ErrUsage, strings.Join(set, ", "))
		}

		return targetFromConfig(flags, deps)
	}

	return targetFromFlags(flags)
}

func targetFromFlags(flags *targetFlags) (*target, error) {
	if flags.bucket == "" || flags.region == "" {
		return nil, fmt.Errorf("%w: give --config, or --bucket and --region", ErrUsage)
	}

	channel := orDefault(flags.channel, defaultChannel)
	name := orDefault(flags.connection, defaultConnection)
	spec := &connection.S3Spec{
		Region:      flags.region,
		Endpoint:    flags.endpoint,
		Credentials: connection.Credentials{Type: connection.CredentialsChain},
		Bucket:      flags.bucket,
		// A custom endpoint (an emulator, an S3 compatible store) rarely serves virtual-hosted bucket names.
		PathStyle: flags.endpoint != "",
	}

	return &target{
		spec: spec, name: name,
		cfg: s3publish.Config{
			Bucket:      flags.bucket,
			Prefix:      flags.prefix,
			Channel:     channel,
			Connection:  name,
			ManifestKey: flags.manifestKey,
		},
	}, nil
}

func targetFromConfig(flags *targetFlags, deps *Deps) (*target, error) {
	opts := bootstrap.LoadOptions{LookupEnv: deps.LookupEnv, ReadFile: deps.ReadFile}

	cfg, err := bootstrap.Load(&bootstrap.Overrides{Config: flags.config}, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", flags.config, err)
	}

	plane := cfg.ControlPlane
	if plane == nil {
		return nil, fmt.Errorf("%s: %w", flags.config, ErrNoControlPlane)
	}

	conn := cfg.Connections[plane.Connection]
	if conn.S3 == nil {
		return nil, fmt.Errorf("%s: connection %q: %w", flags.config, plane.Connection, ErrNotS3)
	}

	spec, err := s3Spec(conn.S3, opts)
	if err != nil {
		return nil, fmt.Errorf("%s: connection %q: %w", flags.config, plane.Connection, err)
	}

	out := s3publish.Config{
		Bucket: conn.S3.Bucket, Prefix: flags.prefix, Channel: plane.Manifest.Channel, Connection: plane.Connection,
		ManifestKey: plane.Manifest.Key,
	}
	if plane.Manifest.MaxObjects != nil {
		out.MaxObjects = *plane.Manifest.MaxObjects
	}

	return &target{spec: spec, name: plane.Connection, cfg: out}, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

// s3Spec turns the S3 connection of the bootstrap file into the spec of the factory, resolving the secrets.
func s3Spec(conn *bootstrap.S3Connection, opts bootstrap.LoadOptions) (*connection.S3Spec, error) {
	creds, err := credentials(conn.Credentials, opts)
	if err != nil {
		return nil, err
	}

	spec := &connection.S3Spec{
		Credentials:                creds,
		Endpoint:                   conn.Endpoint,
		Region:                     conn.Region,
		Bucket:                     conn.Bucket,
		RequestChecksumCalculation: connection.ChecksumMode(conn.RequestChecksumCalculation),
		ResponseChecksumValidation: connection.ChecksumMode(conn.ResponseChecksumValidation),
		PathStyle:                  conn.PathStyle,
	}

	if conn.Timeouts != nil {
		spec.ConnectTimeout = duration(conn.Timeouts.Connect)
		spec.RequestTimeout = duration(conn.Timeouts.Request)
	}

	if conn.Retry != nil {
		spec.Retry.MaxBackoff = duration(conn.Retry.MaxBackoff)

		if conn.Retry.MaxAttempts != nil {
			spec.Retry.MaxAttempts = *conn.Retry.MaxAttempts
		}
	}

	return spec, nil
}

func duration(d *bootstrap.Duration) time.Duration {
	if d == nil {
		return 0
	}

	return time.Duration(*d)
}

func credentials(cred *bootstrap.Credentials, opts bootstrap.LoadOptions) (connection.Credentials, error) {
	if cred == nil {
		return connection.Credentials{Type: connection.CredentialsChain}, nil
	}

	out := connection.Credentials{
		Type:        connection.CredentialsType(cred.Type),
		RoleARN:     cred.RoleARN,
		SessionName: cred.SessionName,
		TokenFile:   cred.TokenFile,
	}

	var err error

	out.ExternalID, err = resolve(cred.ExternalID, opts)
	if err != nil {
		return out, fmt.Errorf("credentials.externalId: %w", err)
	}

	out.AccessKeyID, err = resolveSecret(cred.AccessKeyID, "accessKeyId", opts)
	if err != nil {
		return out, err
	}

	out.SecretAccessKey, err = resolveSecret(cred.SecretAccessKey, "secretAccessKey", opts)
	if err != nil {
		return out, err
	}

	out.SessionToken, err = resolveSecret(cred.SessionToken, "sessionToken", opts)
	if err != nil {
		return out, err
	}

	return out, nil
}

func resolve(s bootstrap.Secret, opts bootstrap.LoadOptions) (string, error) {
	if !s.IsSet() {
		return "", nil
	}

	r, err := s.Resolve(opts)
	if err != nil {
		return "", err //nolint:wrapcheck // names the variable or file, never the value
	}

	return r.Reveal(), nil
}

func resolveSecret(s bootstrap.Secret, field string, opts bootstrap.LoadOptions) (connection.Secret, error) {
	v, err := resolve(s, opts)
	if err != nil {
		return connection.Secret{}, fmt.Errorf("credentials.%s: %w", field, err)
	}

	return connection.NewSecret(v), nil
}
