package bootstrap

// Poll is the polling budget of a connection.
type Poll struct {
	// Interval defaults to 60s.
	Interval *Duration `json:"interval,omitempty" yaml:"interval,omitempty"`
	// Jitter is a fraction between 0 and 1. Default 0.1.
	Jitter *float64 `json:"jitter,omitempty" jsonschema:"minimum=0,maximum=1,default=0.1" yaml:"jitter,omitempty"`
}

// S3Connection is a connection of type s3: one bucket, one endpoint, one credentials identity.
type S3Connection struct {
	AWS `yaml:",inline"`

	Poll   *Poll  `json:"poll,omitempty"   yaml:"poll,omitempty"`
	Type   string `json:"type"             jsonschema:"enum=s3"    yaml:"type"`
	Bucket string `json:"bucket,omitempty" yaml:"bucket,omitempty"`
	// RequestChecksumCalculation is whenSupported (default) or whenRequired.
	RequestChecksumCalculation string `json:"requestChecksumCalculation,omitempty" jsonschema:"enum=whenSupported,enum=whenRequired,default=whenSupported" yaml:"requestChecksumCalculation,omitempty"` //nolint:lll // struct tags can not be wrapped
	// ResponseChecksumValidation is whenSupported (default) or whenRequired.
	ResponseChecksumValidation string `json:"responseChecksumValidation,omitempty" jsonschema:"enum=whenSupported,enum=whenRequired,default=whenSupported" yaml:"responseChecksumValidation,omitempty"` //nolint:lll // struct tags can not be wrapped
	PathStyle                  bool   `json:"pathStyle,omitempty"                  yaml:"pathStyle,omitempty"`
}

// KinesisConnection is a connection of type kinesis.
type KinesisConnection struct {
	AWS `yaml:",inline"`

	Type   string `json:"type"             jsonschema:"enum=kinesis" yaml:"type"`
	Stream string `json:"stream,omitempty" yaml:"stream,omitempty"`
}

// SQSConnection is a connection of type sqs.
type SQSConnection struct {
	AWS `yaml:",inline"`

	Type     string `json:"type"               jsonschema:"enum=sqs"     yaml:"type"`
	QueueURL string `json:"queueUrl,omitempty" yaml:"queueUrl,omitempty"`
}

// RedshiftConnection is a connection of type redshift.
type RedshiftConnection struct {
	AWS `yaml:",inline"`

	Type string `json:"type" jsonschema:"enum=redshift" yaml:"type"`
}

// FileConnection is a connection of type file: a directory that holds the objects of the control plane, for a
// volume that is mounted into the relay (a local publish, a shared file system).
type FileConnection struct {
	Type string `json:"type" jsonschema:"enum=file" yaml:"type"`
	// Path is the root directory. Object keys are resolved below it and can not leave it.
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
}
