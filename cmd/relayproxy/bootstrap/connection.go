package bootstrap

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/invopop/jsonschema"
	"gopkg.in/yaml.v3"
)

// Connection types.
const (
	TypeS3       = "s3"
	TypeKinesis  = "kinesis"
	TypeSQS      = "sqs"
	TypeRedshift = "redshift"
)

// Credential types.
const (
	CredentialsChain       = "chain"
	CredentialsStatic      = "static"
	CredentialsAssumeRole  = "assumeRole"
	CredentialsWebIdentity = "webIdentity"
)

// Checksum settings of the S3 client.
const (
	ChecksumWhenSupported = "whenSupported"
	ChecksumWhenRequired  = "whenRequired"
)

// Connection is a discriminated union on the "type" key: exactly one member is set.
// New connection types are added as one member, one case in [connectionVariant] and one in the validation.
type Connection struct {
	S3       *S3Connection       `json:"-" yaml:"-"`
	Kinesis  *KinesisConnection  `json:"-" yaml:"-"`
	SQS      *SQSConnection      `json:"-" yaml:"-"`
	Redshift *RedshiftConnection `json:"-" yaml:"-"`
}

// Type returns the discriminator of the set member, or "" for an empty connection.
func (c Connection) Type() string {
	switch {
	case c.S3 != nil:
		return TypeS3
	case c.Kinesis != nil:
		return TypeKinesis
	case c.SQS != nil:
		return TypeSQS
	case c.Redshift != nil:
		return TypeRedshift
	}

	return ""
}

// connectionVariant maps a type name to the Go type of its settings. Types that are planned but not built yet
// give [ErrUnsupportedType], other names give [ErrUnknownType].
func connectionVariant(name string) (reflect.Type, error) {
	switch name {
	case TypeS3:
		return reflect.TypeFor[S3Connection](), nil
	case TypeKinesis:
		return reflect.TypeFor[KinesisConnection](), nil
	case TypeSQS:
		return reflect.TypeFor[SQSConnection](), nil
	case TypeRedshift:
		return reflect.TypeFor[RedshiftConnection](), nil
	case "valkey", "redis", "webhook", keyKafka, "postgres", "mysql", "dynamodb", "gcs", "azureBlob", "pubsub",
		"bigquery", "http", "file":
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedType, name)
	}

	return nil, fmt.Errorf("%w: %q", ErrUnknownType, name)
}

// UnmarshalYAML implements [yaml.Unmarshaler]: it dispatches on the "type" key.
func (c *Connection) UnmarshalYAML(node *yaml.Node) error {
	name := mappingValue(node, "type")

	t, err := connectionVariant(name)
	if err != nil {
		return err
	}

	switch t {
	case reflect.TypeFor[S3Connection]():
		c.S3 = new(S3Connection)

		return decodeVariant(node, c.S3)
	case reflect.TypeFor[KinesisConnection]():
		c.Kinesis = new(KinesisConnection)

		return decodeVariant(node, c.Kinesis)
	case reflect.TypeFor[SQSConnection]():
		c.SQS = new(SQSConnection)

		return decodeVariant(node, c.SQS)
	default:
		c.Redshift = new(RedshiftConnection)

		return decodeVariant(node, c.Redshift)
	}
}

// MarshalYAML implements [yaml.Marshaler].
func (c Connection) MarshalYAML() (any, error) { //nolint:unparam // yaml.Marshaler signature
	switch {
	case c.S3 != nil:
		return c.S3, nil
	case c.Kinesis != nil:
		return c.Kinesis, nil
	case c.SQS != nil:
		return c.SQS, nil
	case c.Redshift != nil:
		return c.Redshift, nil
	}

	return map[string]string{}, nil
}

// MarshalJSON implements [json.Marshaler] through the YAML form.
func (c Connection) MarshalJSON() ([]byte, error) {
	v, _ := c.MarshalYAML()

	out, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal connection: %w", err)
	}

	return out, nil
}

// JSONSchema is a oneOf over the members, on the "type" discriminator.
func (Connection) JSONSchema() *jsonschema.Schema {
	names := []string{defS3Connection, "KinesisConnection", "SQSConnection", "RedshiftConnection"}
	one := make([]*jsonschema.Schema, 0, len(names))

	for _, n := range names {
		one = append(one, &jsonschema.Schema{Ref: "#/$defs/" + n})
	}

	return &jsonschema.Schema{OneOf: one}
}

// aws returns the AWS settings shared by every member, or nil.
func (c Connection) aws() *AWS {
	switch {
	case c.S3 != nil:
		return &c.S3.AWS
	case c.Kinesis != nil:
		return &c.Kinesis.AWS
	case c.SQS != nil:
		return &c.SQS.AWS
	case c.Redshift != nil:
		return &c.Redshift.AWS
	}

	return nil
}

// AWS holds the settings every AWS-family connection shares.
type AWS struct {
	Credentials *Credentials `json:"credentials,omitempty" yaml:"credentials,omitempty"`
	Timeouts    *Timeouts    `json:"timeouts,omitempty"    yaml:"timeouts,omitempty"`
	Retry       *Retry       `json:"retry,omitempty"       yaml:"retry,omitempty"`
	// Endpoint overrides the service endpoint, for emulators.
	Endpoint string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	Region   string `json:"region,omitempty"   yaml:"region,omitempty"`
}

// Credentials selects how the AWS client authenticates.
type Credentials struct {
	Type            string `json:"type,omitempty"           jsonschema:"enum=chain,enum=static,enum=assumeRole,enum=webIdentity,default=chain" yaml:"type,omitempty"` //nolint:lll // struct tags can not be wrapped
	RoleARN         string `json:"roleArn,omitempty"        yaml:"roleArn,omitempty"`
	SessionName     string `json:"sessionName,omitempty"    yaml:"sessionName,omitempty"`
	ExternalID      Secret `json:"externalId,omitzero"      yaml:"externalId,omitempty"`
	TokenFile       string `json:"tokenFile,omitempty"      yaml:"tokenFile,omitempty"`
	AccessKeyID     Secret `json:"accessKeyId,omitzero"     yaml:"accessKeyId,omitempty"`
	SecretAccessKey Secret `json:"secretAccessKey,omitzero" yaml:"secretAccessKey,omitempty"`
	SessionToken    Secret `json:"sessionToken,omitzero"    yaml:"sessionToken,omitempty"`
}

// Timeouts of the AWS client.
type Timeouts struct {
	// Connect defaults to 10s.
	Connect *Duration `json:"connect,omitempty" yaml:"connect,omitempty"`
	// Request defaults to 30s.
	Request *Duration `json:"request,omitempty" yaml:"request,omitempty"`
}

// Retry settings of the AWS client.
type Retry struct {
	// MaxAttempts defaults to 3.
	MaxAttempts *int `json:"maxAttempts,omitempty" jsonschema:"minimum=1,maximum=20,default=3" yaml:"maxAttempts,omitempty"` //nolint:lll // struct tags can not be wrapped
	// MaxBackoff defaults to 20s.
	MaxBackoff *Duration `json:"maxBackoff,omitempty" yaml:"maxBackoff,omitempty"`
}

// mappingValue returns the scalar value of key in a mapping node, or "".
func mappingValue(node *yaml.Node, key string) string {
	if node.Kind != yaml.MappingNode {
		return ""
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key && node.Content[i+1].Kind == yaml.ScalarNode {
			return node.Content[i+1].Value
		}
	}

	return ""
}

// decodeVariant decodes the node into the member of the union, after the walker has checked its keys.
func decodeVariant(node *yaml.Node, member any) error {
	err := node.Decode(member)
	if err != nil {
		return fmt.Errorf("decode connection: %w", err)
	}

	return nil
}
