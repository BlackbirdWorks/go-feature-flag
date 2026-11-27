package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"google.golang.org/api/option"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/exporter/azureexporter"
	"github.com/blackbirdworks/semaphore/exporter/bigqueryexporter"
	"github.com/blackbirdworks/semaphore/exporter/fileexporter"
	"github.com/blackbirdworks/semaphore/exporter/gcstorageexporter"
	"github.com/blackbirdworks/semaphore/exporter/kafkaexporter"
	"github.com/blackbirdworks/semaphore/exporter/kinesisexporter"
	"github.com/blackbirdworks/semaphore/exporter/logsexporter"
	"github.com/blackbirdworks/semaphore/exporter/opentelemetryexporter"
	"github.com/blackbirdworks/semaphore/exporter/pubsubexporterv2"
	"github.com/blackbirdworks/semaphore/exporter/redshiftexporter"
	"github.com/blackbirdworks/semaphore/exporter/s3exporterv2"
	"github.com/blackbirdworks/semaphore/exporter/sqsexporter"
	"github.com/blackbirdworks/semaphore/exporter/webhookexporter"
)

// Defaults of the exporters. They are the ones of version 1, a test checks them against it.
const (
	defaultFormat    = "JSON"
	defaultLogFormat = "[{{ .FormattedDate}}] user=\"{{ .UserKey}}\", flag=\"{{ .Key}}\", value=\"{{ .Value}}\""
	defaultFileName  = "flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}"
	defaultCSV       = "{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};" +
		"{{ .Variation}};{{ .Value}};{{ .Default}};{{ .Source}}\n"
	defaultParquetCodec = "SNAPPY"

	eventTypeFeature = "feature"
)

// builtExporter is the exporter a builder made: a struct, so that the builders return a concrete type.
type builtExporter struct {
	exporter.Exporter
}

// ExporterDef is an exporter built from its spec, not started yet.
type ExporterDef struct {
	// Inner is the engine exporter, without the wrapper that counts what it exports. It is sealed: it holds the
	// secrets of the entry in clear.
	Inner Sealed[exporter.Exporter]
	// Name is the name of the entry in the exporters section.
	Name string
	// Kind is the kind of the exporter.
	Kind string
	// Identity changes when anything that affects the exporter changes: its settings, its secrets and the
	// connection it uses. An exporter whose identity did not change is kept by a reload.
	Identity string
	// Config is what [exporter.NewManager] takes. Its Exporter counts what is exported.
	Config exporter.Config
}

// Exporters builds the exporters of the specs, sorted by name. It returns every failing entry, each as an
// [EntryError], and no exporter when one failed.
func (b Builder) Exporters(ctx context.Context, specs Specs) ([]ExporterDef, error) {
	names := slices.Sorted(maps.Keys(specs.Exporters))
	defs := make([]ExporterDef, 0, len(names))

	var errs []error

	for i, name := range names {
		def, err := b.exporter(ctx, name, new(specs.Exporters[name]), specs.Connections)
		if err != nil {
			errs = append(errs, &EntryError{Resource: "exporters", Index: i, Name: name, Err: err})

			continue
		}

		defs = append(defs, def)
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return defs, nil
}

func (b Builder) exporter(
	ctx context.Context, name string, exp *bootstrap.Exporter, conns map[string]bootstrap.Connection,
) (ExporterDef, error) {
	if exp.EventType != "" && exp.EventType != eventTypeFeature {
		return ExporterDef{}, fmt.Errorf("%w: eventType %q", ErrUnsupportedEventType, exp.EventType)
	}

	id, err := newIdentity(exp.Kind, *exp)
	if err != nil {
		return ExporterDef{}, err
	}

	var built builtExporter

	switch exp.Kind {
	case bootstrap.ExporterS3, bootstrap.ExporterKinesis, bootstrap.ExporterSQS, bootstrap.ExporterRedshift:
		built, err = b.awsExporter(ctx, id, exp, conns)
	case bootstrap.ExporterGoogleStorage, bootstrap.ExporterPubSub, bootstrap.ExporterBigQuery,
		bootstrap.ExporterAzure, bootstrap.ExporterKafka:
		built, err = b.remoteExporter(id, exp)
	case bootstrap.ExporterFile, bootstrap.ExporterWebhook, bootstrap.ExporterLog, bootstrap.ExporterOTel:
		built, err = b.localExporter(id, exp)
	default:
		return ExporterDef{}, fmt.Errorf("%w: %q", ErrUnknownKind, exp.Kind)
	}

	if err != nil {
		return ExporterDef{}, err
	}

	return buildDef(name, exp.Kind, id.String(), built.Exporter, exporter.Config{
		Exporter:         nil,
		FlushInterval:    flushInterval(exp),
		MaxEventInMemory: maxEventInMemory(exp),
		MaxQueuedEvents:  0, // the default of the manager
	}, id.secrets), nil
}

// NewExporterDef wraps an engine exporter in a definition the dispatcher can run: the exporter of cfg is
// replaced by a wrapper that counts what it exports. The builder uses it; so does a caller with an exporter of
// its own. Two definitions of one name with the same identity are the same exporter to a reload.
func NewExporterDef(name, kind, identity string, exp exporter.Exporter, cfg exporter.Config) ExporterDef {
	return buildDef(name, kind, identity, exp, cfg, nil)
}

func buildDef(
	name, kind, identity string, exp exporter.Exporter, cfg exporter.Config, secrets []string,
) ExporterDef {
	counter := &countingExporter{inner: exp, instance: &instanceStats{}, secrets: scrubForms(secrets)}
	cfg.Exporter = counter

	return ExporterDef{Name: name, Kind: kind, Identity: identity, Inner: Seal(exp), Config: cfg}
}

// String implements [fmt.Stringer]: the name, the kind and the identity, never the exporter. A definition that
// is printed by value shows no secret either: the exporter it holds is [Sealed].
func (d *ExporterDef) String() string {
	return fmt.Sprintf("ExporterDef{name=%s kind=%s identity=%.8s}", d.Name, d.Kind, d.Identity)
}

// GoString implements [fmt.GoStringer].
func (d *ExporterDef) GoString() string { return d.String() }

// Format implements [fmt.Formatter] so that no verb prints the exporter or its configuration.
func (d *ExporterDef) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(d.String()))
}

// LogValue implements [slog.LogValuer].
func (d *ExporterDef) LogValue() slog.Value {
	return slog.StringValue(d.String())
}

// wrapper returns the counting wrapper that buildDef put in the configuration. It is not a field: a field of the
// definition that holds the wrapper would print the secrets it scrubs when the definition is printed by value.
func (d *ExporterDef) wrapper() *countingExporter {
	wrapper, _ := d.Config.Exporter.(*countingExporter)

	return wrapper
}

func flushInterval(exp *bootstrap.Exporter) time.Duration {
	if exp.FlushInterval != nil && *exp.FlushInterval > 0 {
		return time.Duration(*exp.FlushInterval)
	}

	return bootstrap.DefaultFlushInterval
}

func maxEventInMemory(exp *bootstrap.Exporter) int64 {
	if exp.MaxEventInMemory != nil && *exp.MaxEventInMemory > 0 {
		return int64(*exp.MaxEventInMemory)
	}

	return bootstrap.DefaultMaxEvents
}

func orDefault(value, def string) string {
	if value != "" {
		return value
	}

	return def
}

// fileSettings are the settings the file based exporters share, with their defaults.
type fileSettings struct {
	format, filename, csvTemplate, codec string
}

func newFileSettings(exp *bootstrap.Exporter) fileSettings {
	return fileSettings{
		format:      orDefault(exp.Format, defaultFormat),
		filename:    orDefault(exp.Filename, defaultFileName),
		csvTemplate: orDefault(exp.CsvTemplate, defaultCSV),
		codec:       orDefault(exp.ParquetCompressionCodec, defaultParquetCodec),
	}
}

type field struct{ name, value string }

// firstMissing returns the error of the first required field that is empty.
func firstMissing(fields []field) error {
	for _, f := range fields {
		err := needField(f.name, f.value)
		if err != nil {
			return err
		}
	}

	return nil
}

func needField(field, value string) error {
	if value == "" {
		return settingsError(field, "is required")
	}

	return nil
}

// localExporter builds the exporters that need no remote credentials.
func (b Builder) localExporter(id *identity, exp *bootstrap.Exporter) (builtExporter, error) {
	switch exp.Kind {
	case bootstrap.ExporterFile:
		err := needField("outputDir", exp.OutputDir)
		if err != nil {
			return builtExporter{}, err
		}

		fs := newFileSettings(exp)

		return builtExporter{Exporter: &fileexporter.Exporter{
			Format: fs.format, OutputDir: exp.OutputDir, Filename: fs.filename,
			CsvTemplate: fs.csvTemplate, ParquetCompressionCodec: fs.codec,
		}}, nil
	case bootstrap.ExporterWebhook:
		return b.webhookExporter(id, exp)
	case bootstrap.ExporterLog:
		return builtExporter{
			Exporter: &logsexporter.Exporter{LogFormat: orDefault(exp.LogFormat, defaultLogFormat)},
		}, nil
	default: // bootstrap.ExporterOTel
		return builtExporter{Exporter: &opentelemetryexporter.Exporter{TracerName: exp.TracerName}}, nil
	}
}

func (b Builder) webhookExporter(id *identity, exp *bootstrap.Exporter) (builtExporter, error) {
	if !exp.EndpointURL.IsSet() {
		return builtExporter{}, settingsError("endpointUrl", "is required")
	}

	endpoint, err := b.secret(id, "endpointUrl", exp.EndpointURL)
	if err != nil {
		return builtExporter{}, err
	}

	secret, err := b.secret(id, "secret", exp.Secret)
	if err != nil {
		return builtExporter{}, err
	}

	headers, err := b.headers(id, exp.Headers)
	if err != nil {
		return builtExporter{}, err
	}

	return builtExporter{
		Exporter: &webhookexporter.Exporter{EndpointURL: endpoint, Secret: secret, Meta: exp.Meta, Headers: headers},
	}, nil
}

// remoteExporter builds the exporters of the other clouds and of kafka. Their credentials are settings of the
// exporter, as in version 1: only the AWS kinds have connections.
func (b Builder) remoteExporter(id *identity, exp *bootstrap.Exporter) (builtExporter, error) {
	settings := newFileSettings(exp)

	switch exp.Kind {
	case bootstrap.ExporterGoogleStorage:
		err := needField("bucket", exp.Bucket)
		if err != nil {
			return builtExporter{}, err
		}

		options, err := b.googleOptions(id, exp)
		if err != nil {
			return builtExporter{}, err
		}

		return builtExporter{Exporter: &gcstorageexporter.Exporter{
			Bucket: exp.Bucket, Format: settings.format, Path: exp.Path, Filename: settings.filename,
			CsvTemplate: settings.csvTemplate, ParquetCompressionCodec: settings.codec, Options: options,
		}}, nil
	case bootstrap.ExporterPubSub:
		return b.pubsubExporter(id, exp)
	case bootstrap.ExporterBigQuery:
		return b.bigqueryExporter(id, exp)
	case bootstrap.ExporterAzure:
		return b.azureExporter(id, exp, settings)
	default: // bootstrap.ExporterKafka
		if exp.Kafka == nil || exp.Kafka.Topic == "" || len(exp.Kafka.Addresses) == 0 {
			return builtExporter{}, settingsError("kafka", "needs a topic and addresses")
		}

		kafkaCfg, err := b.kafkaSettings(id, exp.Kafka)
		if err != nil {
			return builtExporter{}, err
		}

		return builtExporter{Exporter: &kafkaexporter.Exporter{Format: settings.format, Settings: kafkaCfg}}, nil
	}
}

// googleOptions turns the credentials of the exporter into client options. Without credentials the client
// takes the application default credentials, as in version 1.
func (b Builder) googleOptions(id *identity, exp *bootstrap.Exporter) ([]option.ClientOption, error) {
	creds, err := b.secret(id, "googleCredentials", exp.GoogleCredentials)
	if err != nil || creds == "" {
		return nil, err
	}

	return []option.ClientOption{option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(creds))}, nil
}

func (b Builder) pubsubExporter(id *identity, exp *bootstrap.Exporter) (builtExporter, error) {
	err := firstMissing([]field{{"projectId", exp.ProjectID}, {"topic", exp.Topic}})
	if err != nil {
		return builtExporter{}, err
	}

	options, err := b.googleOptions(id, exp)
	if err != nil {
		return builtExporter{}, err
	}

	return builtExporter{
		Exporter: &pubsubexporterv2.Exporter{ProjectID: exp.ProjectID, Topic: exp.Topic, Options: options},
	}, nil
}

func (b Builder) bigqueryExporter(id *identity, exp *bootstrap.Exporter) (builtExporter, error) {
	err := firstMissing([]field{{"projectId", exp.ProjectID}, {"datasetId", exp.DatasetID}})
	if err != nil {
		return builtExporter{}, err
	}

	creds, err := b.secret(id, "googleCredentials", exp.GoogleCredentials)
	if err != nil {
		return builtExporter{}, err
	}

	return builtExporter{Exporter: &bigqueryexporter.Exporter{
		ProjectID: exp.ProjectID, DatasetID: exp.DatasetID, TableName: exp.TableName,
		GoogleCredentials: []byte(creds), AutoMigrate: exp.AutoMigrate,
	}}, nil
}

func (b Builder) azureExporter(id *identity, exp *bootstrap.Exporter, settings fileSettings) (builtExporter, error) {
	err := firstMissing([]field{{"container", exp.Container}, {"accountName", exp.AccountName}})
	if err != nil {
		return builtExporter{}, err
	}

	key, err := b.secret(id, "accountKey", exp.AccountKey)
	if err != nil {
		return builtExporter{}, err
	}

	return builtExporter{Exporter: &azureexporter.Exporter{
		Container:               exp.Container,
		Format:                  settings.format,
		Path:                    exp.Path,
		Filename:                settings.filename,
		CsvTemplate:             settings.csvTemplate,
		ParquetCompressionCodec: settings.codec,
		AccountKey:              key,
		AccountName:             exp.AccountName,
	}}, nil
}

// awsExporter builds the exporters that use a connection. The client or the configuration comes from the
// factory, by connection name: the exporter never builds its own and never reads the AWS environment.
func (b Builder) awsExporter(
	ctx context.Context, id *identity, exp *bootstrap.Exporter, conns map[string]bootstrap.Connection,
) (builtExporter, error) {
	var (
		built builtExporter
		conn  bootstrap.Connection
		err   error
	)

	switch exp.Kind {
	case bootstrap.ExporterS3:
		conn, err = connectionOf(conns, exp.Connection, bootstrap.TypeS3)
		if err == nil {
			built, err = b.s3Exporter(ctx, exp, conn)
		}
	case bootstrap.ExporterKinesis:
		conn, err = connectionOf(conns, exp.Connection, bootstrap.TypeKinesis)
		if err == nil {
			built, err = b.kinesisExporter(ctx, exp, conn)
		}
	case bootstrap.ExporterSQS:
		conn, err = connectionOf(conns, exp.Connection, bootstrap.TypeSQS)
		if err == nil {
			built, err = b.sqsExporter(ctx, exp, conn)
		}
	default: // bootstrap.ExporterRedshift
		conn, err = connectionOf(conns, exp.Connection, bootstrap.TypeRedshift)
		if err == nil {
			built, err = b.redshiftExporter(ctx, exp)
		}
	}

	if err != nil {
		return builtExporter{}, err
	}

	// after the factory built the configuration: its build count is part of the identity
	err = b.connectionIdentity(id, exp.Connection, conn)
	if err != nil {
		return builtExporter{}, err
	}

	return built, nil
}

func (b Builder) s3Exporter(
	ctx context.Context,
	exp *bootstrap.Exporter,
	conn bootstrap.Connection,
) (builtExporter, error) {
	if conn.S3.Bucket == "" {
		return builtExporter{}, settingsError("connection", "sets no bucket")
	}

	client, err := b.s3Client(ctx, exp.Connection)
	if err != nil {
		return builtExporter{}, err
	}

	fs := newFileSettings(exp)

	return builtExporter{Exporter: &s3exporterv2.Exporter{
		S3Client: client, Bucket: conn.S3.Bucket, Format: fs.format, S3Path: exp.Path, Filename: fs.filename,
		CsvTemplate: fs.csvTemplate, ParquetCompressionCodec: fs.codec,
	}}, nil
}

func (b Builder) kinesisExporter(
	ctx context.Context,
	exp *bootstrap.Exporter,
	conn bootstrap.Connection,
) (builtExporter, error) {
	name := exp.StreamName
	if name == "" && exp.StreamARN == "" {
		name = conn.Kinesis.Stream
	}

	if name == "" && exp.StreamARN == "" {
		return builtExporter{}, settingsError("streamName", "or streamArn is required (or stream on the connection)")
	}

	cfg, err := b.awsConfig(ctx, exp.Connection)
	if err != nil {
		return builtExporter{}, err
	}

	fs := newFileSettings(exp)

	return builtExporter{Exporter: &kinesisexporter.Exporter{
		Format: fs.format, AwsConfig: &cfg,
		Settings: kinesisexporter.NewSettings(
			kinesisexporter.WithStreamArn(exp.StreamARN), kinesisexporter.WithStreamName(name)),
	}}, nil
}

func (b Builder) sqsExporter(
	ctx context.Context,
	exp *bootstrap.Exporter,
	conn bootstrap.Connection,
) (builtExporter, error) {
	queue := orDefault(exp.QueueURL, conn.SQS.QueueURL)

	err := needField("queueUrl", queue)
	if err != nil {
		return builtExporter{}, err
	}

	cfg, err := b.awsConfig(ctx, exp.Connection)
	if err != nil {
		return builtExporter{}, err
	}

	return builtExporter{Exporter: &sqsexporter.Exporter{QueueURL: queue, AwsConfig: &cfg}}, nil
}

func (b Builder) redshiftExporter(ctx context.Context, exp *bootstrap.Exporter) (builtExporter, error) {
	err := needField("database", exp.Database)
	if err != nil {
		return builtExporter{}, err
	}

	if (exp.ClusterIdentifier == "") == (exp.WorkgroupName == "") {
		return builtExporter{}, settingsError("clusterIdentifier", "or workgroupName: exactly one is required")
	}

	cfg, err := b.awsConfig(ctx, exp.Connection)
	if err != nil {
		return builtExporter{}, err
	}

	return builtExporter{Exporter: &redshiftexporter.Exporter{
		ClusterIdentifier: exp.ClusterIdentifier, WorkgroupName: exp.WorkgroupName, Database: exp.Database,
		DBUser: exp.DBUser, SecretArn: exp.SecretARN, TableName: exp.TableName, AwsConfig: &cfg,
		AutoMigrate: exp.AutoMigrate,
	}}, nil
}
