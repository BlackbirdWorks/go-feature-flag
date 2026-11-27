package bootstrap

// Kafka enumerations.
const (
	// KafkaSASLPlain, KafkaSASLScram256, KafkaSASLScram512 and KafkaSASLGSSAPI are the SASL mechanisms.
	KafkaSASLPlain    = "PLAIN"
	KafkaSASLScram256 = "SCRAM-SHA-256"
	KafkaSASLScram512 = "SCRAM-SHA-512"
	KafkaSASLGSSAPI   = "GSSAPI"

	// KafkaAcksNone, KafkaAcksLocal and KafkaAcksAll are the acknowledgements a producer waits for.
	KafkaAcksNone  = "none"
	KafkaAcksLocal = "local"
	KafkaAcksAll   = "all"

	// KafkaGSSAPIUser and KafkaGSSAPIKeytab are the GSSAPI authentication types.
	KafkaGSSAPIUser   = "user"
	KafkaGSSAPIKeytab = "keytab"
)

// KafkaSettings are the settings of the kafka exporter: the topic and the addresses, and the settings of the
// producer. Version 1 let an operator set any scalar field of the sarama configuration under `kafka.config`; the
// fields a producer uses are keys here. A zero value keeps the sarama default, as in version 1 (a value that is
// not set cannot be told from zero there). The consumer and admin settings, and the fields that hold code (a
// partitioner function, interceptors, a metric registry, a dialer), do not apply to the exporter.
type KafkaSettings struct {
	Net      *KafkaNet      `json:"net,omitempty"      yaml:"net,omitempty"`
	Metadata *KafkaMetadata `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Producer *KafkaProducer `json:"producer,omitempty" yaml:"producer,omitempty"`
	// APIVersionsRequest is sarama `ApiVersionsRequest`.
	APIVersionsRequest *bool `json:"apiVersionsRequest,omitempty" yaml:"apiVersionsRequest,omitempty"`
	// Topic is the topic the events are published to.
	Topic string `json:"topic" yaml:"topic"`
	// Version is the Kafka version, such as 3.6.0 (sarama `Version`).
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
	// ClientID is sarama `ClientID`.
	ClientID string `json:"clientId,omitempty" yaml:"clientId,omitempty"`
	// RackID is sarama `RackID`.
	RackID string `json:"rackId,omitempty" yaml:"rackId,omitempty"`
	// Addresses are the bootstrap brokers, host:port.
	Addresses []string `json:"addresses" yaml:"addresses"`
	// ChannelBufferSize is sarama `ChannelBufferSize`.
	ChannelBufferSize int `json:"channelBufferSize,omitempty" yaml:"channelBufferSize,omitempty"`
}

// KafkaNet is sarama `Net`.
type KafkaNet struct {
	TLS  *KafkaTLS  `json:"tls,omitempty"  yaml:"tls,omitempty"`
	SASL *KafkaSASL `json:"sasl,omitempty" yaml:"sasl,omitempty"`
	// DialTimeout, ReadTimeout, WriteTimeout and KeepAlive are the sarama fields of the same names.
	DialTimeout  *Duration `json:"dialTimeout,omitempty"  yaml:"dialTimeout,omitempty"`
	ReadTimeout  *Duration `json:"readTimeout,omitempty"  yaml:"readTimeout,omitempty"`
	WriteTimeout *Duration `json:"writeTimeout,omitempty" yaml:"writeTimeout,omitempty"`
	KeepAlive    *Duration `json:"keepAlive,omitempty"    yaml:"keepAlive,omitempty"`
	// ResolveCanonicalBootstrapServers is the sarama field of the same name.
	ResolveCanonicalBootstrapServers *bool `json:"resolveCanonicalBootstrapServers,omitempty" yaml:"resolveCanonicalBootstrapServers,omitempty"` //nolint:lll // struct tags can not be wrapped
	// MaxOpenRequests is sarama `MaxOpenRequests`.
	MaxOpenRequests int `json:"maxOpenRequests,omitempty" yaml:"maxOpenRequests,omitempty"`
}

// KafkaTLS is sarama `Net.TLS`. The certificates are files, read when the exporter is built.
type KafkaTLS struct {
	// Enable is sarama `Net.TLS.Enable`.
	Enable *bool `json:"enable,omitempty" yaml:"enable,omitempty"`
	// InsecureSkipVerify is the field of the same name of the TLS configuration.
	InsecureSkipVerify *bool `json:"insecureSkipVerify,omitempty" yaml:"insecureSkipVerify,omitempty"`
	// ServerName is the field of the same name of the TLS configuration.
	ServerName string `json:"serverName,omitempty" yaml:"serverName,omitempty"`
	// CAFile is a PEM file of the authorities that sign the brokers (`RootCAs`).
	CAFile string `json:"caFile,omitempty" yaml:"caFile,omitempty"`
	// CertFile and KeyFile are the PEM files of the client certificate (`Certificates`).
	CertFile string `json:"certFile,omitempty" yaml:"certFile,omitempty"`
	KeyFile  string `json:"keyFile,omitempty"  yaml:"keyFile,omitempty"`
}

// KafkaSASL is sarama `Net.SASL`. Password is `Net.SASL.Password`; Enable, Handshake, Mechanism (PLAIN when
// empty), User, AuthIdentity, SCRAMAuthzID and Version are the fields of the same names.
type KafkaSASL struct {
	GSSAPI       *KafkaGSSAPI `json:"gssapi,omitempty"       yaml:"gssapi,omitempty"`
	Enable       *bool        `json:"enable,omitempty"       yaml:"enable,omitempty"`
	Handshake    *bool        `json:"handshake,omitempty"    yaml:"handshake,omitempty"`
	Mechanism    string       `json:"mechanism,omitempty"    jsonschema:"enum=PLAIN,enum=SCRAM-SHA-256,enum=SCRAM-SHA-512,enum=GSSAPI" yaml:"mechanism,omitempty"` //nolint:lll // struct tags can not be wrapped
	User         string       `json:"user,omitempty"         yaml:"user,omitempty"`
	AuthIdentity string       `json:"authIdentity,omitempty" yaml:"authIdentity,omitempty"`
	SCRAMAuthzID string       `json:"scramAuthzId,omitempty" yaml:"scramAuthzId,omitempty"`
	Password     Secret       `json:"password,omitzero"      yaml:"password,omitempty"`
	Version      int          `json:"version,omitempty"      yaml:"version,omitempty"`
}

// KafkaGSSAPI is sarama `Net.SASL.GSSAPI`, for the GSSAPI mechanism. AuthType is user or keytab; Password is
// `Password`; the other fields are those of the same names.
type KafkaGSSAPI struct {
	DisablePAFXFAST    *bool  `json:"disablePafxfast,omitempty"    yaml:"disablePafxfast,omitempty"`
	AuthType           string `json:"authType,omitempty" jsonschema:"enum=user,enum=keytab" yaml:"authType,omitempty"` //nolint:lll,tagalign // struct tags can not be wrapped
	KeyTabPath         string `json:"keyTabPath,omitempty"         yaml:"keyTabPath,omitempty"`
	KerberosConfigPath string `json:"kerberosConfigPath,omitempty" yaml:"kerberosConfigPath,omitempty"`
	ServiceName        string `json:"serviceName,omitempty"        yaml:"serviceName,omitempty"`
	Username           string `json:"username,omitempty"           yaml:"username,omitempty"`
	Realm              string `json:"realm,omitempty"              yaml:"realm,omitempty"`
	Password           Secret `json:"password,omitzero"            yaml:"password,omitempty"`
}
