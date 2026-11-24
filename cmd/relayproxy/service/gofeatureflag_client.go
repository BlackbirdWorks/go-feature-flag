package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"dario.cat/mergo"
	"github.com/IBM/sarama"
	awsConf "github.com/aws/aws-sdk-go-v2/config"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config/kafka"
	retrieverInit "github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf/init"
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
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/notifier/discordnotifier"
	"github.com/blackbirdworks/semaphore/notifier/microsoftteamsnotifier"
	"github.com/blackbirdworks/semaphore/notifier/slacknotifier"
	"github.com/blackbirdworks/semaphore/notifier/webhooknotifier"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/postgresqlretriever"
	"github.com/blackbirdworks/semaphore/utils"
)

var (
	errProxyConfigIsEmpty                         = errors.New("proxy config is empty")
	errEnableSharedPollerPerBucketRequiresAShared = errors.New(
		"enableSharedPollerPerBucket requires a shared bucket registry",
	)
	errInvalidExporterKind = errors.New("invalid exporter: kind")
	errInvalidNotifierKind = errors.New("invalid notifier: kind")
)

// clientOptions are the internal options of NewGoFeatureFlagClient.
type clientOptions struct {
	sharedRegistry *sharedBucketRegistry
	// postgresPools shares the connection pools of the PostgreSQL retrievers of the clients created with it.
	postgresPools *postgresqlretriever.PoolRegistry
	// newClient builds the engine client. It defaults to ffclient.New and is replaced only by tests.
	newClient func(ffclient.Config) (*ffclient.GoFeatureFlag, error)
	// defaultExporterCleanQueueInterval is used when the flagset does not set its own exporterCleanQueueInterval.
	defaultExporterCleanQueueInterval time.Duration
}

// withClientFactory replaces the function building the engine client (tests observe the configuration it receives).
func withClientFactory(f func(ffclient.Config) (*ffclient.GoFeatureFlag, error)) clientOption {
	return func(o *clientOptions) { o.newClient = f }
}

// withDefaultExporterCleanQueueInterval sets the exporterCleanQueueInterval used by the flagsets that do not
// configure their own (the top-level value in flagset mode).
func withDefaultExporterCleanQueueInterval(d time.Duration) clientOption {
	return func(o *clientOptions) { o.defaultExporterCleanQueueInterval = d }
}

// exporterCleanQueueInterval returns the interval to give to the client: the one of the flagset, else the default.
// 0 means that the client applies its own default.
func exporterCleanQueueInterval(cFlagSet *config.FlagSet, o *clientOptions) time.Duration {
	if cFlagSet.ExporterCleanQueueInterval != 0 {
		return cFlagSet.ExporterCleanQueueInterval
	}

	return o.defaultExporterCleanQueueInterval
}

// clientOption customizes NewGoFeatureFlagClient.
type clientOption func(*clientOptions)

// withPostgresPools sets the registry of the connection pools that the PostgreSQL retrievers of the clients
// share (one pool per database URI, however many flagsets use it).
func withPostgresPools(p *postgresqlretriever.PoolRegistry) clientOption {
	return func(o *clientOptions) { o.postgresPools = p }
}

// ensurePostgresPools gives the retrievers of a client created without a shared registry (the default mode, or a
// test) one registry of their own, so that the PostgreSQL retrievers of the client that use the same URI share a pool.
func (o *clientOptions) ensurePostgresPools() {
	if o.postgresPools == nil {
		o.postgresPools = postgresqlretriever.NewPoolRegistry()
	}
}

// retrieverOptions are the options that the retrievers of a client are created with.
func (o *clientOptions) retrieverOptions() []retrieverInit.Option {
	if o.postgresPools == nil {
		return nil
	}

	return []retrieverInit.Option{retrieverInit.WithPostgresPools(o.postgresPools)}
}

// withSharedBucketRegistry sets the registry providing the retrievers of the flagsets with
// enableSharedPollerPerBucket, so that they are shared between the clients created with the same registry.
func withSharedBucketRegistry(r *sharedBucketRegistry) clientOption {
	return func(o *clientOptions) { o.sharedRegistry = r }
}

func NewGoFeatureFlagClient(
	cFlagSet *config.FlagSet,
	logger *slog.Logger,
	notifiers []notifier.Notifier,
) (*ffclient.GoFeatureFlag, error) {
	return newGoFeatureFlagClient(cFlagSet, logger, notifiers)
}

func newGoFeatureFlagClient(
	cFlagSet *config.FlagSet,
	logger *slog.Logger,
	notifiers []notifier.Notifier,
	opts ...clientOption,
) (_ *ffclient.GoFeatureFlag, retErr error) { //nolint:nonamedreturns // the deferred cleanup reads the returned error
	var err error
	if cFlagSet == nil {
		return nil, errProxyConfigIsEmpty
	}
	var o clientOptions
	for _, opt := range opts {
		opt(&o)
	}

	o.ensurePostgresPools()
	if o.sharedRegistry == nil {
		o.sharedRegistry = standaloneSharedRegistry(cFlagSet, logger)
	}
	retrievers, err := initRetrieversWithRegistry(cFlagSet, o.sharedRegistry, o.retrieverOptions()...)
	if err != nil {
		return nil, err
	}
	// If the client can't be created, the shared-poller retrievers must leave their shared group,
	// otherwise their item stays registered forever. Their Shutdown is idempotent.
	defer releaseSharedPollerRetrievers(retrievers, &retErr)

	exporters, err := initDataExporters(cFlagSet)
	if err != nil {
		return nil, err
	}

	notif := make([]notifier.Notifier, 0, len(notifiers))
	if cFlagSet.Notifiers != nil {
		notif, err = initNotifier(cFlagSet.Notifiers, logger)
		if err != nil {
			return nil, err
		}
	}

	// backward compatibility for the notifier field, it was called "notifier" instead of "notifiers"
	// fixed in version v1.46.0
	//nolint:staticcheck // SA1019: the deprecated setting is still supported
	fixNotifiers := cFlagSet.FixNotifiers
	if len(notif) == 0 && fixNotifiers != nil {
		notif, err = initNotifier(fixNotifiers, logger)
		if err != nil {
			return nil, err
		}
	}
	// end of backward compatibility for the notifier field in version v1.66.0
	notif = append(notif, notifiers...)

	f := ffclient.Config{
		PollingInterval:                 clientPollingInterval(cFlagSet),
		LeveledLogger:                   initLeveledLogger(cFlagSet, logger),
		Context:                         context.Background(),
		Retrievers:                      retrievers,
		Notifiers:                       notif,
		FileFormat:                      cFlagSet.FileFormat,
		DataExporters:                   exporters,
		ExporterCleanQueueInterval:      exporterCleanQueueInterval(cFlagSet, &o),
		StartWithRetrieverError:         cFlagSet.StartWithRetrieverError,
		EnablePollingJitter:             cFlagSet.EnablePollingJitter,
		DisableNotifierOnInit:           cFlagSet.DisableNotifierOnInit,
		EvaluationContextEnrichment:     cFlagSet.EvaluationContextEnrichment,
		PersistentFlagConfigurationFile: cFlagSet.PersistentFlagConfigurationFile,
		Name:                            &cFlagSet.Name,
	}
	newClient := o.newClient
	if newClient == nil {
		newClient = ffclient.New
	}
	client, err := newClient(f)
	if err != nil {
		return nil, err
	}
	if rr, ok := firstSharedRetriever(retrievers); ok {
		rr.SetRefresh(client.ForceRefresh)
	}

	return client, nil
}

// standaloneSharedRegistry returns, for a flagset that opted in to the shared poller but has nothing to share
// with (exported constructor), a registry of its own: the flagset gets a group of its own, which polls with the
// interval and the jitter of the flagset. It returns nil when the flagset did not opt in.
func standaloneSharedRegistry(cFlagSet *config.FlagSet, logger *slog.Logger) *sharedBucketRegistry {
	if !cFlagSet.EnableSharedPollerPerBucket {
		return nil
	}

	return newSharedBucketRegistry(
		nil, time.Duration(cFlagSet.PollingInterval)*time.Millisecond, cFlagSet.EnablePollingJitter, logger)
}

// clientPollingInterval returns the polling interval of the client: with the shared poller the client does not
// poll, the poller of the bucket group refreshes it when its object changed.
func clientPollingInterval(cFlagSet *config.FlagSet) time.Duration {
	if cFlagSet.EnableSharedPollerPerBucket {
		return disabledPollingInterval
	}

	return time.Duration(cFlagSet.PollingInterval) * time.Millisecond
}

// disabledPollingInterval disables the polling of a client, the retriever manager starts no
// polling goroutine for a negative interval.
const disabledPollingInterval = -1

// firstSharedRetriever returns the shared-poller retriever of the flagset, if it has one.
func firstSharedRetriever(retrievers []retriever.Retriever) (*releasingRetriever, bool) {
	if len(retrievers) == 0 {
		return nil, false
	}
	rr, ok := retrievers[0].(*releasingRetriever)

	return rr, ok
}

// releaseSharedPollerRetrievers makes the shared-poller retrievers leave their shared group when the client
// creation failed, otherwise their item stays registered forever. Their Shutdown is idempotent.
func releaseSharedPollerRetrievers(retrievers []retriever.Retriever, retErr *error) {
	if *retErr == nil {
		return
	}
	for _, r := range retrievers {
		if ur, ok := r.(*releasingRetriever); ok {
			_ = ur.Shutdown(context.Background())
		}
	}
}

// initLeveledLogger initializes the leveled logger
// it will add the flagset name as a contextual attribute if it is not the default flagset.
func initLeveledLogger(c *config.FlagSet, logger *slog.Logger) *slog.Logger {
	if c.Name != "" && c.Name != utils.DefaultFlagSetName {
		return logger.With(slog.String("flagset", c.Name))
	}

	return logger
}

// initRetrieversWithRegistry initialize the retrievers based on the configuration, it handles both
// the `retriever` and `retrievers` fields. The registry provides the retriever of the flagsets with
// enableSharedPollerPerBucket, it is required for them.
func initRetrieversWithRegistry(
	proxyConf *config.FlagSet, registry *sharedBucketRegistry, retrieverOpts ...retrieverInit.Option,
) ([]retriever.Retriever, error) {
	retrievers := make([]retriever.Retriever, 0)
	if proxyConf.EnableSharedPollerPerBucket {
		if registry == nil {
			return nil, errEnableSharedPollerPerBucketRequiresAShared
		}
		r, err := registry.retrieverFor(proxyConf)
		if err != nil {
			return nil, err
		}

		return append(retrievers, r), nil
	}
	// if the retriever is set, we add it to the retrievers
	if proxyConf.Retriever != nil {
		currentRetriever, err := retrieverInit.Retriever(proxyConf.Retriever, retrieverOpts...)
		if err != nil {
			return nil, err
		}
		retrievers = append(retrievers, currentRetriever)
	}
	// if the retrievers are set, we add them to the retrievers
	if proxyConf.Retrievers != nil {
		for _, r := range *proxyConf.Retrievers {
			currentRetriever, err := retrieverInit.Retriever(&r, retrieverOpts...)
			if err != nil {
				return nil, err
			}
			retrievers = append(retrievers, currentRetriever)
		}
	}

	return retrievers, nil
}

// initDataExporters initialize the exporters based on the configuration
// it handles both the `exporter` and `exporters` fields.
func initDataExporters(proxyConf *config.FlagSet) ([]ffclient.DataExporter, error) {
	exporters := make([]ffclient.DataExporter, 0)
	if proxyConf.Exporter != nil {
		currentExporter, err := initDataExporter(proxyConf.Exporter)
		if err != nil {
			return nil, err
		}
		exporters = append(exporters, currentExporter)
	}
	if proxyConf.Exporters != nil {
		for _, e := range *proxyConf.Exporters {
			currentExporter, err := initDataExporter(&e)
			if err != nil {
				return nil, err
			}
			exporters = append(exporters, currentExporter)
		}
	}

	return exporters, nil
}

func initDataExporter(c *config.ExporterConf) (ffclient.DataExporter, error) {
	exporterEventType := c.ExporterEventType
	if exporterEventType == "" {
		exporterEventType = config.DefaultExporter().ExporterEventType
	}
	dataExp := ffclient.DataExporter{
		FlushInterval: func() time.Duration {
			if c.FlushInterval != 0 {
				return time.Duration(c.FlushInterval) * time.Millisecond
			}

			return config.DefaultExporter().FlushInterval
		}(),
		MaxEventInMemory: func() int64 {
			if c.MaxEventInMemory != 0 {
				return c.MaxEventInMemory
			}

			return config.DefaultExporter().MaxEventInMemory
		}(),
		ExporterEventType: exporterEventType,
	}

	var err error
	dataExp.Exporter, err = createExporter(c)
	if err != nil {
		return ffclient.DataExporter{}, err
	}

	return dataExp, nil
}

// exporterDefaults are the settings shared by the file based exporters, with their default values.
type exporterDefaults struct {
	format                  string
	filename                string
	csvTemplate             string
	parquetCompressionCodec string
}

func newExporterDefaults(c *config.ExporterConf) exporterDefaults {
	def := config.DefaultExporter()

	return exporterDefaults{
		format:                  valueOrDefault(c.Format, def.Format),
		filename:                valueOrDefault(c.Filename, def.FileName),
		csvTemplate:             valueOrDefault(c.CsvTemplate, def.CsvFormat),
		parquetCompressionCodec: valueOrDefault(c.ParquetCompressionCodec, def.ParquetCompressionCodec),
	}
}

func valueOrDefault(value, def string) string {
	if value != "" {
		return value
	}

	return def
}

func createExporter(c *config.ExporterConf) (exporter.CommonExporter, error) {
	d := newExporterDefaults(c)

	switch c.Kind {
	case config.WebhookExporter:
		return &webhookexporter.Exporter{
			EndpointURL: c.EndpointURL,
			Secret:      c.Secret,
			Meta:        c.Meta,
			Headers:     c.Headers,
		}, nil
	case config.FileExporter:
		return &fileexporter.Exporter{
			Format:                  d.format,
			OutputDir:               c.OutputDir,
			Filename:                d.filename,
			CsvTemplate:             d.csvTemplate,
			ParquetCompressionCodec: d.parquetCompressionCodec,
		}, nil
	case config.LogExporter:
		return &logsexporter.Exporter{LogFormat: valueOrDefault(c.LogFormat, config.DefaultExporter().LogFormat)}, nil
	case config.S3Exporter, config.KinesisExporter, config.SQSExporter, config.RedshiftExporter:
		return createAWSExporter(c, d)
	case config.GoogleStorageExporter:
		return &gcstorageexporter.Exporter{
			Bucket:                  c.Bucket,
			Format:                  d.format,
			Path:                    c.Path,
			Filename:                d.filename,
			CsvTemplate:             d.csvTemplate,
			ParquetCompressionCodec: d.parquetCompressionCodec,
		}, nil
	case config.KafkaExporter:
		settings, err := setKafkaConfig(c.Kafka)
		if err != nil {
			return nil, err
		}

		return &kafkaexporter.Exporter{
			Format:   d.format,
			Settings: settings,
		}, nil
	case config.PubSubExporter:
		return &pubsubexporterv2.Exporter{
			ProjectID: c.ProjectID,
			Topic:     c.Topic,
		}, nil
	case config.BigQueryExporter:
		return &bigqueryexporter.Exporter{
			ProjectID:         c.ProjectID,
			DatasetID:         c.DatasetID,
			TableName:         c.TableName,
			GoogleCredentials: []byte(c.GoogleCredentials),
			AutoMigrate:       c.AutoMigrate,
		}, nil
	case config.AzureExporter:
		return &azureexporter.Exporter{
			Container:               c.Container,
			Format:                  d.format,
			Path:                    c.Path,
			Filename:                d.filename,
			CsvTemplate:             d.csvTemplate,
			ParquetCompressionCodec: d.parquetCompressionCodec,
			AccountKey:              c.AccountKey,
			AccountName:             c.AccountName,
		}, nil
	case config.OpenTelemetryExporter:
		return &opentelemetryexporter.Exporter{
			TracerName: c.TracerName,
		}, nil
	default:
		return nil, fmt.Errorf("%w \"%s\" is not supported", errInvalidExporterKind, c.Kind)
	}
}

// createAWSExporter builds the exporters that load the default AWS configuration.
func createAWSExporter(c *config.ExporterConf, d exporterDefaults) (exporter.CommonExporter, error) {
	var awsOpts []func(*awsConf.LoadOptions) error
	if c.Kind == config.RedshiftExporter && c.AwsRegion != "" {
		awsOpts = append(awsOpts, awsConf.WithRegion(c.AwsRegion))
	}
	awsConfig, err := awsConf.LoadDefaultConfig(context.Background(), awsOpts...)
	if err != nil {
		return nil, err
	}

	switch c.Kind {
	case config.S3Exporter:
		return &s3exporterv2.Exporter{
			Bucket:                  c.Bucket,
			Format:                  d.format,
			S3Path:                  c.Path,
			Filename:                d.filename,
			CsvTemplate:             d.csvTemplate,
			ParquetCompressionCodec: d.parquetCompressionCodec,
			AwsConfig:               &awsConfig,
		}, nil
	case config.KinesisExporter:
		return &kinesisexporter.Exporter{
			Format:    d.format,
			AwsConfig: &awsConfig,
			Settings: kinesisexporter.NewSettings(
				kinesisexporter.WithStreamArn(c.StreamArn),
				kinesisexporter.WithStreamName(c.StreamName),
			),
		}, nil
	case config.SQSExporter:
		return &sqsexporter.Exporter{
			QueueURL:  c.QueueURL,
			AwsConfig: &awsConfig,
		}, nil
	default: // config.RedshiftExporter, the only other kind routed here
		return &redshiftexporter.Exporter{
			ClusterIdentifier: c.ClusterIdentifier,
			WorkgroupName:     c.WorkgroupName,
			Database:          c.Database,
			DBUser:            c.DBUser,
			SecretArn:         c.SecretArn,
			TableName:         c.TableName,
			AwsConfig:         &awsConfig,
			AutoMigrate:       c.AutoMigrate,
		}, nil
	}
}

// setKafkaConfig set the kafka configuration based on the default configuration
// it will initialize the default configuration and merge it with the changes from the user.
func setKafkaConfig(k kafkaexporter.Settings) (kafkaexporter.Settings, error) {
	c := kafkaexporter.Settings{
		Topic:     k.Topic,
		Addresses: k.Addresses,
	}

	if k.Config == nil {
		return c, nil
	}
	saramaConfig := sarama.NewConfig()
	// WithOverride: a value set by the operator wins over the sarama default; a zero value cannot be told from
	// an unset one, so it keeps the default.
	err := mergo.Merge(saramaConfig, k.Config, mergo.WithOverride)
	if err != nil {
		return kafkaexporter.Settings{}, err
	}
	// mergo copies a struct with unexported fields as a whole, so an unset Version would erase the default one.
	if k.Config.Version == (sarama.KafkaVersion{}) {
		saramaConfig.Version = sarama.NewConfig().Version
	}
	// the kafka exporter is using a sync producer, it needs both to be true to start.
	saramaConfig.Producer.Return.Errors = true
	saramaConfig.Producer.Return.Successes = true

	switch saramaConfig.Net.SASL.Mechanism {
	case sarama.SASLTypeSCRAMSHA256:
		saramaConfig.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &kafka.XDGSCRAMClient{HashGeneratorFcn: kafka.SHA256}
		}
	case sarama.SASLTypeSCRAMSHA512:
		saramaConfig.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &kafka.XDGSCRAMClient{HashGeneratorFcn: kafka.SHA512}
		}
	}
	c.Config = saramaConfig

	return c, nil
}

func initNotifier(c []config.NotifierConf, logger *slog.Logger) ([]notifier.Notifier, error) {
	if c == nil {
		return nil, nil
	}
	var notifiers []notifier.Notifier

	for _, cNotif := range c {
		switch cNotif.Kind {
		case config.SlackNotifier:
			if cNotif.WebhookURL == "" &&
				cNotif.SlackWebhookURL != "" { //nolint:staticcheck // SA1019: the deprecated setting is still supported
				logger.Warn("slackWebhookURL field is deprecated, please use webhookURL instead")
				cNotif.WebhookURL = cNotif.SlackWebhookURL //nolint:staticcheck // SA1019: the deprecated setting is still supported
			}
			notifiers = append(
				notifiers,
				&slacknotifier.Notifier{SlackWebhookURL: cNotif.WebhookURL},
			)
		case config.MicrosoftTeamsNotifier:
			notifiers = append(
				notifiers,
				&microsoftteamsnotifier.Notifier{
					MicrosoftTeamsWebhookURL: cNotif.WebhookURL,
				},
			)
		case config.WebhookNotifier:
			notifiers = append(notifiers,
				&webhooknotifier.Notifier{
					Secret:      cNotif.Secret,
					EndpointURL: cNotif.EndpointURL,
					Meta:        cNotif.Meta,
					Headers:     cNotif.Headers,
				},
			)
		case config.DiscordNotifier:
			notifiers = append(
				notifiers,
				&discordnotifier.Notifier{DiscordWebhookURL: cNotif.WebhookURL},
			)
		default:
			return nil, fmt.Errorf("%w \"%s\" is not supported", errInvalidNotifierKind, cNotif.Kind)
		}
	}

	return notifiers, nil
}
