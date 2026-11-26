package connection

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
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
//
// No lock is held while a callback runs (logger, observer, ReadFile, Spec methods); a build holds only the lock
// of its own entry, and a reload waits for a build in progress on a replaced entry before closing it.
type Factory struct {
	httpClient   aws.HTTPClient
	roundTripper http.RoundTripper
	observer     RequestObserver
	logger       *slog.Logger
	readFile     func(string) ([]byte, error)
	// afterLookup is a test hook: it runs between the lookup of an entry and the lock of its build.
	afterLookup func(name string)

	entries map[string]*entry
	builds  map[string]int
	mu      sync.Mutex
}

// entry is the built state of one connection at one identity.
type entry struct {
	cfg *aws.Config
	s3  *s3.Client
	// owned is the HTTP client the factory built for this entry; nil when the caller supplied it.
	owned *http.Client
	// roots are the root CAs parsed when the spec was accepted; nil means the system roots.
	roots    *x509.CertPool
	spec     Spec
	kind     string
	identity string
	mu       sync.Mutex
	// retired is set under mu when the entry left the factory: nothing is built on it any more.
	retired bool
}

// retire marks the entry as gone and closes the idle connections of the HTTP client the factory built for
// it. It waits for a build in progress, whose result is then closed too.
func (e *entry) retire() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.retired = true

	if e.owned != nil {
		e.owned.CloseIdleConnections()
	}
}

// prepared is a spec that passed validation, with what was read once to accept it.
type prepared struct {
	spec     Spec
	roots    *x509.CertPool
	kind     string
	identity string
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
// closed). The CA bundle of the spec, if any, is read and parsed here, once. An invalid spec changes nothing.
func (f *Factory) Set(name string, spec Spec) error {
	prep, err := f.prepare(name, spec)
	if err != nil {
		return err
	}

	f.mu.Lock()
	stale := f.put(name, prep)
	f.mu.Unlock()

	closeAll(stale)

	return nil
}

// Replace makes specs the whole set of connections, as a reload does: a connection whose identity did not
// change keeps its client, a changed one gets a new client on next use, and a connection that is not in
// specs is dropped. The idle connections of every dropped or replaced client are closed. An invalid spec
// changes nothing.
func (f *Factory) Replace(specs map[string]Spec) error {
	preps := make(map[string]prepared, len(specs))

	for name, spec := range specs {
		prep, err := f.prepare(name, spec)
		if err != nil {
			return err
		}

		preps[name] = prep
	}

	var stale []*entry

	f.mu.Lock()

	for name, ent := range f.entries {
		if _, ok := preps[name]; !ok {
			delete(f.entries, name)

			stale = append(stale, ent)
		}
	}

	for name, prep := range preps {
		stale = append(stale, f.put(name, prep)...)
	}

	f.mu.Unlock()

	closeAll(stale)

	return nil
}

// Remove drops the connection name and closes the idle connections of its client; it is a no-op for an
// unknown name. A caller that was about to build the client of the removed connection gets
// [ErrUnknownConnection] (or the client of the connection if the name was added again meanwhile).
func (f *Factory) Remove(name string) {
	f.mu.Lock()
	ent := f.entries[name]
	delete(f.entries, name)
	f.mu.Unlock()

	if ent != nil {
		ent.retire()
	}
}

func closeAll(entries []*entry) {
	for _, ent := range entries {
		ent.retire()
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
//
// The copy shares nothing mutable with the cached configuration, except what is shared on purpose: the HTTP
// client (the one connection pool of the connection) and the credentials provider (the one credentials
// cache). The provider prints as [RedactedValue].
func (f *Factory) AWSConfig(ctx context.Context, name string) (aws.Config, error) {
	var out aws.Config

	err := f.withEntry(ctx, name, false, func(_ *entry, cfg *aws.Config) {
		out = copyConfig(cfg)
	})
	if err != nil {
		return aws.Config{}, err
	}

	return out, nil
}

// copyConfig copies cfg and clones the pointers and slices the factory set in it.
func copyConfig(cfg *aws.Config) aws.Config {
	copied := cfg.Copy()
	copied.APIOptions = slices.Clone(cfg.APIOptions)

	if cfg.BaseEndpoint != nil {
		copied.BaseEndpoint = aws.String(*cfg.BaseEndpoint)
	}

	return copied
}

// S3 returns the S3 client of the connection name: the same client for every caller until the identity of
// the connection changes.
func (f *Factory) S3(ctx context.Context, name string) (*s3.Client, error) {
	var client *s3.Client

	err := f.withEntry(ctx, name, true, func(ent *entry, cfg *aws.Config) {
		// withEntry checked the kind (wantS3): an entry that is not S3 never gets here
		if spec, isS3 := ent.spec.(*S3Spec); isS3 && ent.s3 == nil {
			ent.s3 = s3.NewFromConfig(*cfg, func(opts *s3.Options) {
				opts.UsePathStyle = spec.PathStyle
				// the spec endpoint wins over any service endpoint the SDK would resolve by itself
				if spec.Endpoint != "" {
					opts.BaseEndpoint = aws.String(spec.Endpoint)
				}
			})
		}

		client = ent.s3
	})
	if err != nil {
		return nil, err
	}

	return client, nil
}

// withEntry runs use on the current entry of name with its configuration built, under the lock of the entry.
// An entry retired between the lookup and the lock is skipped: the lookup then finds the entry that replaced
// it, or fails with [ErrUnknownConnection]. The build is logged after the lock is released.
func (f *Factory) withEntry(ctx context.Context, name string, wantS3 bool, use func(*entry, *aws.Config)) error {
	for {
		ent, err := f.lookup(name)
		if err != nil {
			return err
		}

		if f.afterLookup != nil {
			f.afterLookup(name)
		}

		built, err := f.useEntry(ctx, name, ent, wantS3, use)
		if errors.Is(err, errRetired) {
			continue
		}

		if built && f.logger != nil {
			f.logger.LogAttrs(ctx, slog.LevelDebug, "connection client built",
				slog.String("connection", name), slog.String("kind", ent.kind))
		}

		return err
	}
}

// errRetired is the internal signal that the entry left the factory before the build started.
var errRetired = errors.New("entry retired")

// useEntry is the locked part of [Factory.withEntry]; built reports that this call built the configuration.
func (f *Factory) useEntry(
	ctx context.Context, name string, ent *entry, wantS3 bool, use func(*entry, *aws.Config),
) (bool, error) {
	ent.mu.Lock()
	defer ent.mu.Unlock()

	if ent.retired {
		return false, errRetired
	}

	if _, isS3 := ent.spec.(*S3Spec); wantS3 && !isS3 {
		return false, &KindError{Name: name, Want: KindS3, Got: ent.kind}
	}

	built := ent.cfg == nil

	cfg, err := f.config(ctx, name, ent)
	if err != nil {
		return false, err
	}

	use(ent, cfg)

	return built, nil
}

// put must be called with f.mu held. It returns the entry it replaced, if any.
func (f *Factory) put(name string, prep prepared) []*entry {
	cur, ok := f.entries[name]
	if ok && cur.identity == prep.identity {
		return nil
	}

	f.entries[name] = &entry{spec: prep.spec, kind: prep.kind, identity: prep.identity, roots: prep.roots}

	if ok {
		return []*entry{cur}
	}

	return nil
}

// prepare validates spec and reads its CA bundle, once. No lock is held: Validate, Identity and ReadFile
// are callbacks. The identity of the entry is the one of the spec plus a digest of the bundle content, so a
// bundle changed under the same path rebuilds the client on the next reload.
func (f *Factory) prepare(name string, spec Spec) (prepared, error) {
	if name == "" {
		return prepared{}, &SpecError{Field: "name", Reason: "must not be empty"}
	}

	if spec == nil {
		return prepared{}, &SpecError{Connection: name, Field: "spec", Reason: "is nil"}
	}

	err := spec.Validate()
	if err != nil {
		if se, ok := errors.AsType[*SpecError](err); ok {
			named := *se
			named.Connection = name

			return prepared{}, &named
		}

		return prepared{}, fmt.Errorf("connection %q: %w", name, err)
	}

	prep := prepared{spec: spec, kind: spec.Kind(), identity: spec.Identity()}

	backed, ok := spec.(awsBacked)
	if !ok || backed.common().CABundleFile == "" {
		return prep, nil
	}

	data, err := f.readFile(backed.common().CABundleFile)
	if err != nil {
		return prepared{}, &SpecError{
			Connection: name, Field: fieldCABundle, Reason: fmt.Sprintf("cannot read the bundle: %v", err),
		}
	}

	prep.roots, err = parseRoots(data)
	if err != nil {
		return prepared{}, &SpecError{Connection: name, Field: fieldCABundle, Reason: err.Error()}
	}

	sum := sha256.Sum256(data)
	prep.identity += ":ca:" + hex.EncodeToString(sum[:])

	return prep, nil
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
		return nil, &KindError{Name: name, Want: "an AWS backed kind", Got: ent.kind}
	}

	cfg, owned, err := f.build(ctx, name, backed, ent.roots)
	if err != nil {
		return nil, err
	}

	ent.cfg = &cfg
	ent.owned = owned

	f.mu.Lock()
	f.builds[name]++
	f.mu.Unlock()

	return ent.cfg, nil
}

// build makes the AWS configuration of a connection. Everything of the data plane (region, endpoint, HTTP
// transport, retries, checksums, defaults) comes from the spec and the factory, never from the process
// environment or the shared config files. The SDK default chain is used only to obtain a credentials
// provider, for credentials of type chain and for assumeRole without source keys.
func (f *Factory) build(
	ctx context.Context, name string, spec awsBacked, roots *x509.CertPool,
) (aws.Config, *http.Client, error) {
	com := spec.common()
	choice := f.newHTTPClient(com, roots)

	var err error

	cfg := aws.Config{Region: com.Region, HTTPClient: choice.client}

	switch {
	case usesChain(&com.Credentials):
		cfg.Credentials, err = chainCredentials(ctx, name, com.Region)
		if err != nil {
			return aws.Config{}, nil, err
		}
	case com.Credentials.Type == CredentialsStatic || com.Credentials.Type == CredentialsAssumeRole:
		cfg.Credentials = staticCredentials(&com.Credentials)
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

// chainCredentials returns the credentials provider of the SDK default chain, wrapped so it never prints.
// The SDK loads its own HTTP client for it (so AWS_CA_BUNDLE applies to credential lookups only); nothing else
// of the loaded configuration is used.
//
// Limit: the SDK parses and validates every AWS_* variable (checksum, defaults mode, retry, ...) and the shared
// files when it loads the chain, and no load option skips that. A chain connection therefore fails to build
// when the process AWS environment is invalid, even for a variable unrelated to credentials; the error names
// the connection and says so. Static, webIdentity and assumeRole-with-keys connections never load it.
func chainCredentials(ctx context.Context, name, region string) (redactedProvider, error) {
	loaded, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region))
	if err != nil {
		return redactedProvider{}, fmt.Errorf(
			"connection %q: the AWS credentials chain cannot be loaded, the process AWS environment or shared "+
				"config is invalid: %w", name, err)
	}

	return newRedacted(loaded.Credentials), nil
}

// usesChain reports whether the credentials come from the SDK default chain.
func usesChain(creds *Credentials) bool {
	switch creds.Type {
	case "", CredentialsChain:
		return true
	case CredentialsAssumeRole:
		return creds.AccessKeyID.IsZero()
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
		cfg.Credentials = newRedacted(aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(client, creds.RoleARN,
			func(o *stscreds.AssumeRoleOptions) {
				o.RoleSessionName = creds.SessionName
				if creds.ExternalID != "" {
					o.ExternalID = aws.String(creds.ExternalID)
				}
			})))
	case CredentialsWebIdentity:
		anonymous := cfg.Copy()
		anonymous.Credentials = aws.AnonymousCredentials{}
		client := sts.NewFromConfig(anonymous, stsEndpoint(creds))
		cfg.Credentials = newRedacted(aws.NewCredentialsCache(stscreds.NewWebIdentityRoleProvider(client, creds.RoleARN,
			stscreds.IdentityTokenFile(creds.TokenFile), func(o *stscreds.WebIdentityRoleOptions) {
				o.RoleSessionName = creds.SessionName
			})))
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
func (f *Factory) newHTTPClient(com *Common, roots *x509.CertPool) httpChoice {
	if f.httpClient != nil {
		return httpChoice{client: f.httpClient}
	}

	if f.roundTripper != nil {
		client := &http.Client{Transport: f.roundTripper, CheckRedirect: NoRedirect}

		return httpChoice{client: client, owned: client}
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

	return httpChoice{client: client, owned: client}
}

// parseRoots returns the system roots plus the PEM certificates of data.
func parseRoots(data []byte) (*x509.CertPool, error) {
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
