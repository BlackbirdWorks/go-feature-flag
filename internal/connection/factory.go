package connection

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Option configures a [Factory].
type Option func(*Factory)

// WithHTTPClient makes every client use client instead of one HTTP client per connection (tests). The
// timeouts of the specs and the redirect policy are then the business of client: to keep the request
// metric exact, it must not follow redirects (see [NoRedirect]). The factory never closes it.
func WithHTTPClient(client aws.HTTPClient) Option {
	return func(f *Factory) { f.httpClient = client }
}

// WithRoundTripper makes every client use rt as the transport of an HTTP client of its own (tests). That
// client does not follow redirects. The timeouts of the specs do not apply (they configure the transport
// the factory builds itself), and when a connection is dropped or replaced the factory calls
// CloseIdleConnections on rt if it has the method.
func WithRoundTripper(rt http.RoundTripper) Option {
	return func(f *Factory) { f.roundTripper = rt }
}

// WithReadFile sets the function that reads the CA bundle files of the specs (default [os.ReadFile]).
func WithReadFile(readFile func(string) ([]byte, error)) Option {
	return func(f *Factory) { f.readFile = readFile }
}

// WithObserver reports every HTTP attempt of the AWS clients to obs.
func WithObserver(obs RequestObserver) Option {
	return func(f *Factory) { f.observer = obs }
}

// WithLogger sets the logger of the factory (client builds, at debug level). It never receives a secret.
func WithLogger(logger *slog.Logger) Option {
	return func(f *Factory) { f.logger = logger }
}

// NoRedirect is the [http.Client.CheckRedirect] of every client the factory builds: a 3xx answer is
// returned to the SDK instead of followed, so a redirect never turns one SDK attempt into several requests.
func NoRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// Factory holds the specs of the connections by name and builds exactly one AWS configuration (and S3
// client) per connection and identity. It is safe for concurrent use.
type Factory struct {
	httpClient   aws.HTTPClient
	roundTripper http.RoundTripper
	observer     RequestObserver
	logger       *slog.Logger
	readFile     func(string) ([]byte, error)

	entries map[string]*entry
	builds  map[string]int
	mu      sync.Mutex
}

// entry is the built state of one connection at one identity.
type entry struct {
	cfg *aws.Config
	s3  *s3.Client
	// owned is the HTTP client the factory built for this entry; nil when the caller supplied it.
	owned    *http.Client
	spec     Spec
	identity string
	mu       sync.Mutex
}

// closeIdle closes the idle connections of the HTTP client the factory built for the entry.
func (e *entry) closeIdle() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.owned != nil {
		e.owned.CloseIdleConnections()
	}
}

// New returns an empty factory; load its connections with [Factory.Replace] or [Factory.Set].
func New(opts ...Option) *Factory {
	fac := &Factory{
		entries:  make(map[string]*entry),
		builds:   make(map[string]int),
		readFile: os.ReadFile,
	}
	for _, opt := range opts {
		opt(fac)
	}

	return fac
}

// Set adds or updates the connection name. The client built for it is kept when the identity of spec is
// the one already held, and rebuilt on next use otherwise (the idle connections of the old one are
// closed). An invalid spec changes nothing.
func (f *Factory) Set(name string, spec Spec) error {
	err := f.validateNamed(name, spec)
	if err != nil {
		return err
	}

	f.mu.Lock()
	stale := f.put(name, spec)
	f.mu.Unlock()

	closeAll(stale)

	return nil
}

// Replace makes specs the whole set of connections, as a reload does: a connection whose identity did not
// change keeps its client, a changed one gets a new client on next use, and a connection that is not in
// specs is dropped. The idle connections of every dropped or replaced client are closed. An invalid spec
// changes nothing.
func (f *Factory) Replace(specs map[string]Spec) error {
	for name, spec := range specs {
		err := f.validateNamed(name, spec)
		if err != nil {
			return err
		}
	}

	var stale []*entry

	f.mu.Lock()

	for name, ent := range f.entries {
		if _, ok := specs[name]; !ok {
			delete(f.entries, name)

			stale = append(stale, ent)
		}
	}

	for name, spec := range specs {
		stale = append(stale, f.put(name, spec)...)
	}

	f.mu.Unlock()

	closeAll(stale)

	return nil
}

// Remove drops the connection name and closes the idle connections of its client; it is a no-op for an
// unknown name.
func (f *Factory) Remove(name string) {
	f.mu.Lock()
	ent := f.entries[name]
	delete(f.entries, name)
	f.mu.Unlock()

	if ent != nil {
		ent.closeIdle()
	}
}

func closeAll(entries []*entry) {
	for _, ent := range entries {
		ent.closeIdle()
	}
}

// String implements [fmt.Stringer]: the factory prints its size only, never its specs.
func (f *Factory) String() string {
	return fmt.Sprintf("connection.Factory{%d connections}", len(f.Names()))
}

// GoString implements [fmt.GoStringer].
func (f *Factory) GoString() string { return f.String() }

// Names returns the connection names, sorted.
func (f *Factory) Names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Sorted(maps.Keys(f.entries))
}

// Builds returns, per connection name, how many AWS configurations (and with them the S3 client) the
// factory built for it since it was created. A connection used by any number of callers and never changed
// has one. A caller that builds several service clients (SQS, Kinesis, ...) from one [Factory.AWSConfig]
// is not counted: the factory only sees the configuration.
func (f *Factory) Builds() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return maps.Clone(f.builds)
}

// AWSConfig returns a copy of the AWS configuration of the connection name, built once per identity. It
// serves any AWS backed connection (S3, Kinesis, SQS, Redshift). The configuration carries the endpoint
// and region of the spec and ignores the process environment and shared config files for them (and for
// service endpoints), but a service client built from it must not be given another endpoint.
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

	cp := cfg.Copy()
	cp.APIOptions = slices.Clone(cfg.APIOptions)

	return cp, nil
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

	ent.s3 = s3.NewFromConfig(*cfg, func(opts *s3.Options) {
		opts.UsePathStyle = spec.PathStyle
		// the spec endpoint wins over any service endpoint the SDK would resolve by itself
		if spec.Endpoint != "" {
			opts.BaseEndpoint = aws.String(spec.Endpoint)
		}
	})

	return ent.s3, nil
}

// put must be called with f.mu held. It returns the entry it replaced, if any.
func (f *Factory) put(name string, spec Spec) []*entry {
	identity := spec.Identity()

	cur, ok := f.entries[name]
	if ok && cur.identity == identity {
		return nil
	}

	f.entries[name] = &entry{spec: spec, identity: identity}

	if ok {
		return []*entry{cur}
	}

	return nil
}

func (f *Factory) validateNamed(name string, spec Spec) error {
	if name == "" {
		return &SpecError{Field: "name", Reason: "must not be empty"}
	}

	if spec == nil {
		return &SpecError{Connection: name, Field: "spec", Reason: "is nil"}
	}

	err := spec.Validate()
	if err != nil {
		if se, ok := errors.AsType[*SpecError](err); ok {
			named := *se
			named.Connection = name

			return &named
		}

		return fmt.Errorf("connection %q: %w", name, err)
	}

	if backed, ok := spec.(awsBacked); ok {
		_, err = f.rootPool(backed.common().CABundleFile)
		if err != nil {
			return &SpecError{Connection: name, Field: fieldCABundle, Reason: err.Error()}
		}
	}

	return nil
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

// config builds the AWS configuration of ent once; ent.mu must be held.
func (f *Factory) config(ctx context.Context, name string, ent *entry) (*aws.Config, error) {
	if ent.cfg != nil {
		return ent.cfg, nil
	}

	backed, ok := ent.spec.(awsBacked)
	if !ok {
		return nil, &KindError{Name: name, Want: "an AWS backed kind", Got: ent.spec.Kind()}
	}

	cfg, owned, err := f.build(ctx, name, backed)
	if err != nil {
		return nil, err
	}

	ent.cfg = &cfg
	ent.owned = owned

	f.mu.Lock()
	f.builds[name]++
	f.mu.Unlock()

	if f.logger != nil {
		f.logger.LogAttrs(ctx, slog.LevelDebug, "connection client built",
			slog.String("connection", name), slog.String("kind", ent.spec.Kind()))
	}

	return ent.cfg, nil
}

// build makes the AWS configuration of a connection. Everything of the data plane (region, endpoint, HTTP
// transport, retries, checksums, defaults) comes from the spec and the factory, never from the process
// environment or the shared config files. The SDK default chain is used only to obtain a credentials
// provider, for credentials of type chain and for assumeRole without source keys.
func (f *Factory) build(ctx context.Context, name string, spec awsBacked) (aws.Config, *http.Client, error) {
	com := spec.common()

	choice, err := f.newHTTPClient(com)
	if err != nil {
		return aws.Config{}, nil, &SpecError{Connection: name, Field: fieldCABundle, Reason: err.Error()}
	}

	cfg := aws.Config{Region: com.Region, HTTPClient: choice.client}

	switch {
	case usesChain(&com.Credentials):
		cfg.Credentials, err = chainCredentials(ctx, com.Region)
		if err != nil {
			return aws.Config{}, nil, fmt.Errorf("connection %q: %w", name, err)
		}
	case com.Credentials.Type == CredentialsStatic || com.Credentials.Type == CredentialsAssumeRole:
		cfg.Credentials = credentials.NewStaticCredentialsProvider(
			com.Credentials.AccessKeyID.Reveal(), com.Credentials.SecretAccessKey.Reveal(),
			com.Credentials.SessionToken.Reveal())
	case com.Credentials.Type == CredentialsWebIdentity:
	}

	cfg.DefaultsMode = aws.DefaultsModeLegacy

	if com.Endpoint != "" {
		cfg.BaseEndpoint = aws.String(com.Endpoint)
	}

	cfg.Retryer = retryer(com.Retry)
	cfg.RequestChecksumCalculation, cfg.ResponseChecksumValidation = checksums(spec)

	assumeRole(&cfg, &com.Credentials)

	if f.observer != nil {
		cfg.APIOptions = append(cfg.APIOptions, observerOption(name, f.observer))
	}

	return cfg, choice.owned, nil
}

// chainCredentials returns the credentials provider of the SDK default chain. The SDK loads its own HTTP
// client for it (so AWS_CA_BUNDLE and the proxy variables apply to credential lookups only); nothing else of
// the loaded configuration is used.
func chainCredentials(ctx context.Context, region string) (*aws.CredentialsCache, error) {
	loaded, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load the AWS credentials chain: %w", err)
	}

	return aws.NewCredentialsCache(loaded.Credentials), nil
}

// usesChain reports whether the credentials come from the SDK default chain.
func usesChain(creds *Credentials) bool {
	switch creds.Type {
	case "", CredentialsChain:
		return true
	case CredentialsAssumeRole:
		return creds.AccessKeyID == ""
	case CredentialsStatic, CredentialsWebIdentity:
	}

	return false
}

func retryer(retries Retry) func() aws.Retryer {
	return func() aws.Retryer {
		return retry.NewStandard(func(opts *retry.StandardOptions) {
			if retries.MaxAttempts > 0 {
				opts.MaxAttempts = retries.MaxAttempts
			}

			if retries.MaxBackoff > 0 {
				opts.MaxBackoff = retries.MaxBackoff
			}
		})
	}
}

// checksums resolves the two checksum settings; the SDK default (whenSupported) when unset or not S3.
func checksums(spec awsBacked) (aws.RequestChecksumCalculation, aws.ResponseChecksumValidation) {
	request := aws.RequestChecksumCalculationWhenSupported
	response := aws.ResponseChecksumValidationWhenSupported

	s3spec, ok := spec.(*S3Spec)
	if !ok {
		return request, response
	}

	if s3spec.RequestChecksumCalculation == ChecksumWhenRequired {
		request = aws.RequestChecksumCalculationWhenRequired
	}

	if s3spec.ResponseChecksumValidation == ChecksumWhenRequired {
		response = aws.ResponseChecksumValidationWhenRequired
	}

	return request, response
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

// httpChoice is the HTTP client of a connection, and the client the factory built (and so closes), if any.
type httpChoice struct {
	client aws.HTTPClient
	owned  *http.Client
}

// newHTTPClient returns the HTTP client of one connection. The client built from scratch has its own
// connection pool and a transport bounding the TCP connect (ConnectTimeout), the TLS handshake and the wait
// for the response headers (RequestTimeout); the reading of the body is bounded by the context of the caller
// only. It never follows redirects.
func (f *Factory) newHTTPClient(com *Common) (httpChoice, error) {
	if f.httpClient != nil {
		return httpChoice{client: f.httpClient}, nil
	}

	if f.roundTripper != nil {
		client := &http.Client{Transport: f.roundTripper, CheckRedirect: NoRedirect}

		return httpChoice{client: client, owned: client}, nil
	}

	roots, err := f.rootPool(com.CABundleFile)
	if err != nil {
		return httpChoice{}, err
	}

	connect := defaultConnectTimeout
	if com.ConnectTimeout > 0 {
		connect = com.ConnectTimeout
	}

	transport := &http.Transport{
		TLSClientConfig:       &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DialContext:           (&net.Dialer{Timeout: connect, KeepAlive: dialKeepAlive}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          maxIdleConns,
		IdleConnTimeout:       idleConnTimeout,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: com.RequestTimeout,
	}
	client := &http.Client{Transport: transport, CheckRedirect: NoRedirect}

	return httpChoice{client: client, owned: client}, nil
}

// rootPool returns the root CAs of the transport: nil (the system roots) without a bundle, the system
// roots plus the bundle with one.
func (f *Factory) rootPool(bundle string) (*x509.CertPool, error) {
	if bundle == "" {
		return nil, nil //nolint:nilnil // nil RootCAs means the system roots
	}

	data, err := f.readFile(bundle)
	if err != nil {
		return nil, fmt.Errorf("cannot read the bundle: %w", err)
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}

	if !pool.AppendCertsFromPEM(data) {
		return nil, errNoCertificate
	}

	return pool, nil
}

const fieldCABundle = "caBundleFile"

var errNoCertificate = errors.New("the bundle holds no PEM certificate")

const (
	defaultConnectTimeout = 30 * time.Second
	dialKeepAlive         = 30 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	idleConnTimeout       = 90 * time.Second
	maxIdleConns          = 100
)
