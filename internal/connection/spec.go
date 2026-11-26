package connection

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"net/url"
	"strconv"
	"time"
)

const fieldCredentials = "credentials"

// Kind names of the AWS backed connections the factory builds.
const (
	// KindS3 is an S3 (or S3 compatible) bucket.
	KindS3 = "s3"
	// KindKinesis is a Kinesis stream backend.
	KindKinesis = "kinesis"
	// KindSQS is an SQS backend.
	KindSQS = "sqs"
	// KindRedshift is a Redshift data API backend.
	KindRedshift = "redshift"
)

// Spec describes one connection. The AWS backed kinds ([S3Spec], [AWSSpec]) live in this package and are the
// only ones the [Factory] can build clients for; a backend that is not AWS gets its own spec type and its own
// factory method later, without changing the callers of the existing ones.
type Spec interface {
	// Kind is the connection type (s3, kinesis, sqs, redshift, ...).
	Kind() string
	// Identity is a stable fingerprint of everything that affects the client: two specs with the same
	// identity build interchangeable clients. It never contains a secret in clear.
	Identity() string
	// Validate reports the first invalid field, as a [SpecError].
	Validate() error
}

// CredentialsType selects how the credentials of a connection are resolved.
type CredentialsType string

// The credentials types.
const (
	// CredentialsChain is the SDK default chain (environment, shared files, container, instance role).
	// It is the default when the type is empty.
	CredentialsChain CredentialsType = "chain"
	// CredentialsStatic is a fixed access key.
	CredentialsStatic CredentialsType = "static"
	// CredentialsAssumeRole assumes a role with the chain credentials as the source.
	CredentialsAssumeRole CredentialsType = "assumeRole"
	// CredentialsWebIdentity assumes a role with a web identity token file.
	CredentialsWebIdentity CredentialsType = "webIdentity"
)

// Credentials holds the credentials settings of a connection. Secret material is already resolved.
type Credentials struct {
	// Type defaults to chain.
	Type CredentialsType `json:"type"`
	// AccessKeyID, SecretAccessKey and SessionToken are the static credentials (SessionToken is optional).
	// With assumeRole they are the source credentials; without them the chain is the source.
	AccessKeyID     Secret `json:"accessKeyId"`
	SecretAccessKey Secret `json:"secretAccessKey"`
	SessionToken    Secret `json:"sessionToken"`
	// RoleARN is the role of assumeRole and webIdentity.
	RoleARN string `json:"roleArn"`
	// SessionName is the role session name (assumeRole, webIdentity); optional.
	SessionName string `json:"sessionName"`
	// ExternalID is the external id of assumeRole; optional.
	ExternalID string `json:"externalId"`
	// TokenFile is the web identity token file path.
	TokenFile string `json:"tokenFile"`
	// STSEndpoint overrides the STS endpoint of assumeRole and webIdentity; optional.
	STSEndpoint string `json:"stsEndpoint"`
}

// ChecksumMode is the value of a checksum setting.
type ChecksumMode string

// The checksum modes. The empty value keeps the SDK default.
const (
	// ChecksumDefault keeps the SDK default.
	ChecksumDefault ChecksumMode = ""
	// ChecksumWhenSupported computes (or validates) a checksum whenever the operation supports it.
	ChecksumWhenSupported ChecksumMode = "whenSupported"
	// ChecksumWhenRequired computes (or validates) a checksum only when the operation requires it.
	ChecksumWhenRequired ChecksumMode = "whenRequired"
)

// Retry holds the retry settings. A zero value keeps the SDK default.
type Retry struct {
	// MaxAttempts is the maximum number of attempts of one operation, the first one included.
	MaxAttempts int `json:"maxAttempts"`
	// MaxBackoff is the longest wait between two attempts.
	MaxBackoff time.Duration `json:"maxBackoff"`
}

// Common holds the settings shared by every AWS backed connection.
type Common struct {
	Credentials Credentials `json:"credentials"`
	// Endpoint is the service endpoint (emulators, S3 compatible stores); empty means the AWS endpoint of
	// the region. Environment endpoint variables (AWS_ENDPOINT_URL, AWS_ENDPOINT_URL_S3, ...) are ignored.
	Endpoint string `json:"endpoint"`
	// Region is the AWS region. It is required: a connection never takes its region from the process
	// environment or the shared config files.
	Region string `json:"region"`
	Retry  Retry  `json:"retry"`
	// ConnectTimeout bounds the TCP connect only; zero means 30s. The TLS handshake is bounded by 10s.
	// It is ignored when the factory has an HTTP client or round tripper override.
	ConnectTimeout time.Duration `json:"connectTimeout"`
	// RequestTimeout bounds the wait for the response headers of one HTTP attempt, once the request is
	// written; zero means no limit besides the context. It does not bound the reading of the body, which
	// only the context of the caller cancels. It is ignored when the factory has an HTTP client or round
	// tripper override.
	RequestTimeout time.Duration `json:"requestTimeout"`
}

// S3Spec is the spec of an S3 connection. Pass it to the factory as a pointer and do not change it after.
//
//nolint:govet // fieldalignment: the embedded Common must come first (embeddedstructfieldcheck)
type S3Spec struct {
	Common

	// Bucket is the bucket of the connection; it is not required by the factory, the users of the
	// connection require it when they need one.
	Bucket string `json:"bucket"`
	// RequestChecksumCalculation and ResponseChecksumValidation default to the SDK default.
	RequestChecksumCalculation ChecksumMode `json:"requestChecksumCalculation"`
	ResponseChecksumValidation ChecksumMode `json:"responseChecksumValidation"`
	PathStyle                  bool         `json:"pathStyle"`
}

// AWSSpec is the spec of a Kinesis, SQS or Redshift connection. Pass it to the factory as a pointer and do
// not change it after.
//
//nolint:govet // fieldalignment: the embedded Common must come first (embeddedstructfieldcheck)
type AWSSpec struct {
	Common

	// Type is the connection kind: [KindKinesis], [KindSQS] or [KindRedshift].
	Type string `json:"type"`
}

// awsBacked is implemented by the specs the factory can turn into an [aws.Config].
type awsBacked interface {
	Spec
	common() *Common
}

// Kind implements [Spec].
func (*S3Spec) Kind() string { return KindS3 }

// Kind implements [Spec].
func (s *AWSSpec) Kind() string {
	if s == nil {
		return ""
	}

	return s.Type
}

// Validate implements [Spec].
func (s *S3Spec) Validate() error {
	if s == nil {
		return errNilSpec()
	}

	err := s.Common.validate()
	if err != nil {
		return err
	}

	err = validateChecksum("requestChecksumCalculation", s.RequestChecksumCalculation)
	if err != nil {
		return err
	}

	return validateChecksum("responseChecksumValidation", s.ResponseChecksumValidation)
}

// Validate implements [Spec].
func (s *AWSSpec) Validate() error {
	if s == nil {
		return errNilSpec()
	}

	switch s.Type {
	case KindKinesis, KindSQS, KindRedshift:
	default:
		return &SpecError{Field: "type", Reason: fmt.Sprintf("must be kinesis, sqs or redshift, got %q", s.Type)}
	}

	return s.Common.validate()
}

// Identity implements [Spec].
func (s *S3Spec) Identity() string {
	if s == nil {
		return ""
	}

	fprint := newFingerprint(KindS3)
	s.Common.fingerprint(fprint)
	fprint.str("bucket", s.Bucket)
	fprint.str("pathStyle", strconv.FormatBool(s.PathStyle))
	fprint.str("requestChecksumCalculation", string(s.RequestChecksumCalculation))
	fprint.str("responseChecksumValidation", string(s.ResponseChecksumValidation))

	return fprint.sum()
}

// Identity implements [Spec].
func (s *AWSSpec) Identity() string {
	if s == nil {
		return ""
	}

	fprint := newFingerprint(s.Type)
	s.Common.fingerprint(fprint)

	return fprint.sum()
}

func (s *S3Spec) common() *Common  { return &s.Common }
func (s *AWSSpec) common() *Common { return &s.Common }

func errNilSpec() error { return &SpecError{Field: "spec", Reason: "is nil"} }

func validateChecksum(field string, mode ChecksumMode) error {
	switch mode {
	case ChecksumDefault, ChecksumWhenSupported, ChecksumWhenRequired:
		return nil
	}

	return &SpecError{Field: field, Reason: fmt.Sprintf("must be whenSupported or whenRequired, got %q", string(mode))}
}

func (c *Common) validate() error {
	if c.Region == "" {
		return &SpecError{Field: "region", Reason: "is required"}
	}

	err := validateURL("endpoint", c.Endpoint)
	if err != nil {
		return err
	}

	if c.ConnectTimeout < 0 || c.RequestTimeout < 0 {
		return &SpecError{Field: "timeouts", Reason: "must not be negative"}
	}

	if c.Retry.MaxAttempts < 0 || c.Retry.MaxBackoff < 0 {
		return &SpecError{Field: "retry", Reason: "must not be negative"}
	}

	return c.Credentials.validate()
}

func validateURL(field, raw string) error {
	if raw == "" {
		return nil
	}

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return &SpecError{Field: field, Reason: "must be an http or https URL"}
	}

	return nil
}

func (c *Credentials) validateAssumeRole() error {
	if c.RoleARN == "" {
		return &SpecError{Field: fieldCredentials, Reason: "assumeRole needs roleArn"}
	}

	if (c.AccessKeyID == "") != (c.SecretAccessKey == "") || (c.AccessKeyID == "" && c.SessionToken != "") {
		return &SpecError{
			Field:  fieldCredentials,
			Reason: "assumeRole source credentials need accessKeyId and secretAccessKey together",
		}
	}

	return nil
}

func (c *Credentials) validate() error {
	switch c.Type {
	case "", CredentialsChain:
	case CredentialsStatic:
		if c.AccessKeyID == "" || c.SecretAccessKey == "" {
			return &SpecError{
				Field:  fieldCredentials,
				Reason: "static credentials need accessKeyId and secretAccessKey",
			}
		}
	case CredentialsAssumeRole:
		err := c.validateAssumeRole()
		if err != nil {
			return err
		}
	case CredentialsWebIdentity:
		if c.RoleARN == "" || c.TokenFile == "" {
			return &SpecError{Field: fieldCredentials, Reason: "webIdentity needs roleArn and tokenFile"}
		}
	default:
		return &SpecError{Field: "credentials.type", Reason: fmt.Sprintf("unknown type %q", string(c.Type))}
	}

	return validateURL("credentials.stsEndpoint", c.STSEndpoint)
}

func (c *Common) fingerprint(fprint *fingerprint) {
	fprint.str("endpoint", c.Endpoint)
	fprint.str("region", c.Region)
	fprint.str("connectTimeout", c.ConnectTimeout.String())
	fprint.str("requestTimeout", c.RequestTimeout.String())
	fprint.str("retry.maxAttempts", strconv.Itoa(c.Retry.MaxAttempts))
	fprint.str("retry.maxBackoff", c.Retry.MaxBackoff.String())

	c.Credentials.fingerprint(fprint)
}

// fingerprint writes the credential settings that matter for the type: leftover fields of another type do
// not change the identity, and an empty type is chain.
func (c *Credentials) fingerprint(fprint *fingerprint) {
	credType := c.Type
	if credType == "" {
		credType = CredentialsChain
	}

	fprint.str("credentials.type", string(credType))

	switch credType {
	case CredentialsStatic:
		fprint.secret("credentials.accessKeyId", c.AccessKeyID)
		fprint.secret("credentials.secretAccessKey", c.SecretAccessKey)
		fprint.secret("credentials.sessionToken", c.SessionToken)
	case CredentialsAssumeRole:
		fprint.secret("credentials.accessKeyId", c.AccessKeyID)
		fprint.secret("credentials.secretAccessKey", c.SecretAccessKey)
		fprint.secret("credentials.sessionToken", c.SessionToken)
		fprint.str("credentials.roleArn", c.RoleARN)
		fprint.str("credentials.sessionName", c.SessionName)
		fprint.str("credentials.externalId", c.ExternalID)
		fprint.str("credentials.stsEndpoint", c.STSEndpoint)
	case CredentialsWebIdentity:
		fprint.str("credentials.roleArn", c.RoleARN)
		fprint.str("credentials.sessionName", c.SessionName)
		fprint.str("credentials.tokenFile", c.TokenFile)
		fprint.str("credentials.stsEndpoint", c.STSEndpoint)
	case CredentialsChain:
	}
}

// fingerprint writes length prefixed fields (so no two field lists collide) to a SHA-256. A secret enters
// as its own SHA-256, and the result is hashed again: the identity never contains a secret in clear.
type fingerprint struct {
	h hash.Hash
}

func newFingerprint(kind string) *fingerprint {
	f := &fingerprint{h: sha256.New()}
	f.str("kind", kind)

	return f
}

func (f *fingerprint) str(key, value string) {
	_, _ = fmt.Fprintf(f.h, "%d:%s=%d:%s;", len(key), key, len(value), value)
}

func (f *fingerprint) secret(key string, value Secret) {
	if value == "" {
		f.str(key, "")

		return
	}

	sum := sha256.Sum256([]byte(value))
	f.str(key, "sha256:"+hex.EncodeToString(sum[:]))
}

func (f *fingerprint) sum() string {
	return hex.EncodeToString(f.h.Sum(nil))
}
