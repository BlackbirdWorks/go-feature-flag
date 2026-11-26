package connection

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Option configures a [Factory].
type Option func(*Factory)

// WithHTTPClient makes every client use client instead of one HTTP client per connection (tests). The
// connect and request timeouts of the specs are then the business of client.
func WithHTTPClient(client aws.HTTPClient) Option {
	return func(f *Factory) { f.httpClient = client }
}

// WithRoundTripper makes every client use rt as the transport of its HTTP client (tests). The request
// timeout of the spec still applies, the connect timeout does not.
func WithRoundTripper(rt http.RoundTripper) Option {
	return func(f *Factory) { f.roundTripper = rt }
}

// WithObserver reports every HTTP attempt of the AWS clients to obs.
func WithObserver(obs RequestObserver) Option {
	return func(f *Factory) { f.observer = obs }
}

// WithLogger sets the logger of the factory (client builds, at debug level). It never receives a secret.
func WithLogger(logger *slog.Logger) Option {
	return func(f *Factory) { f.logger = logger }
}

// Factory holds the specs of the connections by name and builds exactly one client per connection and
// identity. It is safe for concurrent use.
type Factory struct {
	httpClient   aws.HTTPClient
	roundTripper http.RoundTripper
	observer     RequestObserver
	logger       *slog.Logger

	entries map[string]*entry
	builds  map[string]int
	mu      sync.Mutex
}

// entry is the built state of one connection at one identity.
type entry struct {
	cfg      *aws.Config
	s3       *s3.Client
	spec     Spec
	identity string
	mu       sync.Mutex
}

// New returns an empty factory; load its connections with [Factory.Replace] or [Factory.Set].
func New(opts ...Option) *Factory {
	fac := &Factory{
		entries: make(map[string]*entry),
		builds:  make(map[string]int),
	}
	for _, opt := range opts {
		opt(fac)
	}

	return fac
}

// Set adds or updates the connection name. The client built for it is kept when the identity of spec is
// the one already held, and rebuilt on next use otherwise. An invalid spec changes nothing.
func (f *Factory) Set(name string, spec Spec) error {
	err := validateNamed(name, spec)
	if err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.put(name, spec)

	return nil
}

// Replace makes specs the whole set of connections, as a reload does: a connection whose identity did not
// change keeps its client, a changed one gets a new client on next use, and a connection that is not in
// specs is dropped. An invalid spec changes nothing.
func (f *Factory) Replace(specs map[string]Spec) error {
	for name, spec := range specs {
		err := validateNamed(name, spec)
		if err != nil {
			return err
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	for name := range f.entries {
		if _, ok := specs[name]; !ok {
			delete(f.entries, name)
		}
	}

	for name, spec := range specs {
		f.put(name, spec)
	}

	return nil
}

// Remove drops the connection name; it is a no-op for an unknown name.
func (f *Factory) Remove(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.entries, name)
}

func validateNamed(name string, spec Spec) error {
	if spec == nil {
		return &SpecError{Connection: name, Field: "spec", Reason: "is nil"}
	}

	err := spec.Validate()
	if err != nil {
		if se, ok := err.(*SpecError); ok { //nolint:errorlint // own type, returned unwrapped by Validate
			named := *se
			named.Connection = name

			return &named
		}

		return fmt.Errorf("connection %q: %w", name, err)
	}

	return nil
}

// String implements [fmt.Stringer]: the factory prints its size only, never its specs.
func (f *Factory) String() string {
	return fmt.Sprintf("connection.Factory{%d connections}", len(f.Names()))
}

// GoString implements [fmt.GoStringer].
func (f *Factory) GoString() string { return f.String() }

// Names returns the connection names.
func (f *Factory) Names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	names := make([]string, 0, len(f.entries))
	for name := range f.entries {
		names = append(names, name)
	}

	return names
}

// Builds returns, per connection name, how many clients the factory built for it since it was created. A
// connection used by any number of callers and never changed has one.
func (f *Factory) Builds() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return maps.Clone(f.builds)
}

// AWSConfig returns the AWS configuration of the connection name, built once per identity. It serves any
// AWS backed connection (S3, Kinesis, SQS, Redshift).
func (f *Factory) AWSConfig(ctx context.Context, name string) (aws.Config, error) {
	ent, err := f.lookup(name)
	if err != nil {
		return aws.Config{}, err
	}

	ent.mu.Lock()
	defer ent.mu.Unlock()

	cfg, err := f.config(ctx, name, ent)
	if err != nil {
		return aws.Config{}, err
	}

	return *cfg, nil
}

// S3 returns the S3 client of the connection name: the same client for every caller until the identity of
// the connection changes.
func (f *Factory) S3(ctx context.Context, name string) (*s3.Client, error) {
	ent, err := f.lookup(name)
	if err != nil {
		return nil, err
	}

	spec, ok := ent.spec.(*S3Spec)
	if !ok {
		return nil, &KindError{Name: name, Want: KindS3, Got: ent.spec.Kind()}
	}

	ent.mu.Lock()
	defer ent.mu.Unlock()

	if ent.s3 != nil {
		return ent.s3, nil
	}

	cfg, err := f.config(ctx, name, ent)
	if err != nil {
		return nil, err
	}

	ent.s3 = s3.NewFromConfig(*cfg, func(o *s3.Options) { o.UsePathStyle = spec.PathStyle })

	return ent.s3, nil
}

// put must be called with f.mu held.
func (f *Factory) put(name string, spec Spec) {
	identity := spec.Identity()
	if cur, ok := f.entries[name]; ok && cur.identity == identity {
		return
	}

	f.entries[name] = &entry{spec: spec, identity: identity}
}

func (f *Factory) lookup(name string) (*entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ent, ok := f.entries[name]
	if !ok {
		return nil, &UnknownConnectionError{Name: name}
	}

	return ent, nil
}

// config builds the AWS configuration of e once; ent.mu must be held.
func (f *Factory) config(ctx context.Context, name string, ent *entry) (*aws.Config, error) {
	if ent.cfg != nil {
		return ent.cfg, nil
	}

	backed, ok := ent.spec.(awsBacked)
	if !ok {
		return nil, &KindError{Name: name, Want: "an AWS backed kind", Got: ent.spec.Kind()}
	}

	cfg, err := f.build(ctx, name, backed)
	if err != nil {
		return nil, err
	}

	ent.cfg = &cfg

	f.mu.Lock()
	f.builds[name]++
	f.mu.Unlock()

	if f.logger != nil {
		f.logger.LogAttrs(ctx, slog.LevelDebug, "connection client built",
			slog.String("connection", name), slog.String("kind", ent.spec.Kind()),
			slog.String("identity", ent.identity))
	}

	return ent.cfg, nil
}

func (f *Factory) build(ctx context.Context, name string, spec awsBacked) (aws.Config, error) {
	com := spec.common()

	opts := []func(*config.LoadOptions) error{f.httpClientOption(com)}

	if com.Region != "" {
		opts = append(opts, config.WithRegion(com.Region))
	}

	if com.Endpoint != "" {
		opts = append(opts, config.WithBaseEndpoint(com.Endpoint))
	}

	// Static keys are the credentials of a static connection, and the source of an assumeRole one (the
	// chain is the source when no key is given).
	if com.Credentials.Type == CredentialsStatic ||
		(com.Credentials.Type == CredentialsAssumeRole && com.Credentials.AccessKeyID != "") {
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			com.Credentials.AccessKeyID.Reveal(), com.Credentials.SecretAccessKey.Reveal(),
			com.Credentials.SessionToken.Reveal())))
	}

	if com.Retry.MaxAttempts > 0 || com.Retry.MaxBackoff > 0 {
		opts = append(opts, config.WithRetryer(func() aws.Retryer {
			return retry.NewStandard(func(opts *retry.StandardOptions) {
				if com.Retry.MaxAttempts > 0 {
					opts.MaxAttempts = com.Retry.MaxAttempts
				}

				if com.Retry.MaxBackoff > 0 {
					opts.MaxBackoff = com.Retry.MaxBackoff
				}
			})
		}))
	}

	if s3spec, ok := spec.(*S3Spec); ok {
		opts = append(opts, checksumOptions(s3spec)...)
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("connection %q: load AWS config: %w", name, err)
	}

	assumeRole(&cfg, &com.Credentials)

	if f.observer != nil {
		cfg.APIOptions = append(cfg.APIOptions, observerOption(name, f.observer))
	}

	return cfg, nil
}

func checksumOptions(spec *S3Spec) []func(*config.LoadOptions) error {
	var opts []func(*config.LoadOptions) error

	switch spec.RequestChecksumCalculation {
	case ChecksumWhenSupported:
		opts = append(opts, config.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenSupported))
	case ChecksumWhenRequired:
		opts = append(opts, config.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired))
	case ChecksumDefault:
	}

	switch spec.ResponseChecksumValidation {
	case ChecksumWhenSupported:
		opts = append(opts, config.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenSupported))
	case ChecksumWhenRequired:
		opts = append(opts, config.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired))
	case ChecksumDefault:
	}

	return opts
}

// assumeRole replaces the credentials of cfg for the assumeRole and webIdentity types. The STS client is
// built before the observer is attached, so STS calls are not counted as requests of the connection.
func assumeRole(cfg *aws.Config, creds *Credentials) {
	switch creds.Type {
	case CredentialsAssumeRole:
		client := sts.NewFromConfig(*cfg, stsEndpoint(creds))
		cfg.Credentials = aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(client, creds.RoleARN,
			func(o *stscreds.AssumeRoleOptions) {
				o.RoleSessionName = creds.SessionName
				if creds.ExternalID != "" {
					o.ExternalID = aws.String(creds.ExternalID)
				}
			}))
	case CredentialsWebIdentity:
		anonymous := cfg.Copy()
		anonymous.Credentials = aws.AnonymousCredentials{}
		client := sts.NewFromConfig(anonymous, stsEndpoint(creds))
		cfg.Credentials = aws.NewCredentialsCache(stscreds.NewWebIdentityRoleProvider(client, creds.RoleARN,
			stscreds.IdentityTokenFile(creds.TokenFile), func(o *stscreds.WebIdentityRoleOptions) {
				o.RoleSessionName = creds.SessionName
			}))
	case "", CredentialsChain, CredentialsStatic:
	}
}

// stsEndpoint points STS to its own endpoint: the endpoint of the connection is the one of the data
// service, not of STS.
func stsEndpoint(creds *Credentials) func(*sts.Options) {
	return func(opts *sts.Options) {
		if creds.STSEndpoint != "" {
			opts.BaseEndpoint = aws.String(creds.STSEndpoint)
		} else {
			opts.BaseEndpoint = nil
		}
	}
}

// httpClientOption sets the HTTP client of one connection: the override of the factory, or a client of its
// own, so connection pools are never shared between connections.
func (f *Factory) httpClientOption(com *Common) config.LoadOptionsFunc {
	switch {
	case f.httpClient != nil:
		return config.WithHTTPClient(f.httpClient)
	case f.roundTripper != nil:
		return config.WithHTTPClient(&http.Client{Transport: f.roundTripper, Timeout: com.RequestTimeout})
	}

	client := awshttp.NewBuildableClient()
	if com.RequestTimeout > 0 {
		client = client.WithTimeout(com.RequestTimeout)
	}

	if com.ConnectTimeout > 0 {
		client = client.WithDialerOptions(func(d *net.Dialer) { d.Timeout = com.ConnectTimeout })
	}

	return config.WithHTTPClient(client)
}
