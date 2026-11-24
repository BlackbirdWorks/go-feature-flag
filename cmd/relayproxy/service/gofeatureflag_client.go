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
	slogzap "github.com/samber/slog-zap/v2"
	"go.uber.org/zap"

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

// clientOptions are the internal options of NewGoFeatureFlagClient.
type clientOptions struct {
	sharedRegistry *sharedBucketRegistry
	// postgresPools shares the connection pools of the PostgreSQL retrievers of the clients created with it.
	postgresPools *postgresqlretriever.PoolRegistry
	// defaultExporterCleanQueueInterval is used when the flagset does not set its own exporterCleanQueueInterval.
	defaultExporterCleanQueueInterval time.Duration
	// newClient builds the engine client. It defaults to ffclient.New and is replaced only by tests.
	newClient func(ffclient.Config) (*ffclient.GoFeatureFlag, error)
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
	logger *zap.Logger,
	notifiers []notifier.Notifier,
) (*ffclient.GoFeatureFlag, error) {
	return newGoFeatureFlagClient(cFlagSet, logger, notifiers)
}

func newGoFeatureFlagClient(
	cFlagSet *config.FlagSet,
	logger *zap.Logger,
	notifiers []notifier.Notifier,
	opts ...clientOption,
) (_ *ffclient.GoFeatureFlag, retErr error) {
	var err error
	if cFlagSet == nil {
		return nil, errors.New("proxy config is empty")
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
	if len(notif) == 0 && cFlagSet.FixNotifiers != nil { // nolint: staticcheck
		notif, err = initNotifier(cFlagSet.FixNotifiers, logger) // nolint: staticcheck
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
func standaloneSharedRegistry(cFlagSet *config.FlagSet, logger *zap.Logger) *sharedBucketRegistry {
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
func initLeveledLogger(c *config.FlagSet, logger *zap.Logger) *slog.Logger {
	baseHandler := slogzap.Option{Level: slog.LevelDebug, Logger: logger}.NewZapHandler()
	if c.Name != "" && c.Name != utils.DefaultFlagSetName {
		attrs := []slog.Attr{slog.String("flagset", c.Name)}
		baseHandler = baseHandler.WithAttrs(attrs)
	}

	return slog.New(baseHandler)
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
			return nil, errors.New("enableSharedPollerPerBucket requires a shared bucket registry")
		}
		r, err := registry.retrieverFor(proxyConf)
		if err != nil {
			return nil, err
		}

		return append(retrievers, r), nil
	}
	// if the retriever is set, we add it to the retrievers
	if proxyConf.Retriever != nil {
		currentRetriever, err := retrieverInit.InitRetriever(proxyConf.Retriever, retrieverOpts...)
		if err != nil {
			return nil, err
		}
		retrievers = append(retrievers, currentRetriever)
	}
	// if the retrievers are set, we add them to the retrievers
	if proxyConf.Retrievers != nil {
		for _, r := range *proxyConf.Retrievers {
			currentRetriever, err := retrieverInit.InitRetriever(&r, retrieverOpts...)
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

// nolint: funlen
func createExporter(c *config.ExporterConf) (exporter.CommonExporter, error) {
	format := config.DefaultExporter().Format
	if c.Format != "" {
		format = c.Format
	}

	filename := config.DefaultExporter().FileName
	if c.Filename != "" {
		filename = c.Filename
	}

	csvTemplate := config.DefaultExporter().CsvFormat
	if c.CsvTemplate != "" {
		csvTemplate = c.CsvTemplate
	}

	parquetCompressionCodec := config.DefaultExporter().ParquetCompressionCodec
	if c.ParquetCompressionCodec != "" {
		parquetCompressionCodec = c.ParquetCompressionCodec
	}

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
			Format:                  format,
			OutputDir:               c.OutputDir,
			Filename:                filename,
			CsvTemplate:             csvTemplate,
			ParquetCompressionCodec: parquetCompressionCodec,
		}, nil
	case config.LogExporter:
		return &logsexporter.Exporter{
			LogFormat: func() string {
				if c.LogFormat != "" {
					return c.LogFormat
				}

				return config.DefaultExporter().LogFormat
			}(),
		}, nil
	case config.S3Exporter:
		awsConfig, err := awsConf.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, err
		}

		return &s3exporterv2.Exporter{
			Bucket:                  c.Bucket,
			Format:                  format,
			S3Path:                  c.Path,
			Filename:                filename,
			CsvTemplate:             csvTemplate,
			ParquetCompressionCodec: parquetCompressionCodec,
			AwsConfig:               &awsConfig,
		}, nil
	case config.KinesisExporter:
		awsConfig, err := awsConf.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, err
		}

		return &kinesisexporter.Exporter{
			Format:    format,
			AwsConfig: &awsConfig,
			Settings: kinesisexporter.NewSettings(
				kinesisexporter.WithStreamArn(c.StreamArn),
				kinesisexporter.WithStreamName(c.StreamName),
			),
		}, nil
	case config.GoogleStorageExporter:
		return &gcstorageexporter.Exporter{
			Bucket:                  c.Bucket,
			Format:                  format,
			Path:                    c.Path,
			Filename:                filename,
			CsvTemplate:             csvTemplate,
			ParquetCompressionCodec: parquetCompressionCodec,
		}, nil
	case config.SQSExporter:
		awsConfig, err := awsConf.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, err
		}

		return &sqsexporter.Exporter{
			QueueURL:  c.QueueURL,
			AwsConfig: &awsConfig,
		}, nil
	case config.KafkaExporter:
		settings, err := setKafkaConfig(c.Kafka)
		if err != nil {
			return nil, err
		}

		return &kafkaexporter.Exporter{
			Format:   format,
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
	case config.RedshiftExporter:
		var awsOpts []func(*awsConf.LoadOptions) error
		if c.AwsRegion != "" {
			awsOpts = append(awsOpts, awsConf.WithRegion(c.AwsRegion))
		}
		awsConfig, err := awsConf.LoadDefaultConfig(context.Background(), awsOpts...)
		if err != nil {
			return nil, err
		}

		return &redshiftexporter.Exporter{
			ClusterIdentifier: c.ClusterIdentifier,
			WorkgroupName:     c.WorkgroupName,
			Database:          c.Database,
			DbUser:            c.DbUser,
			SecretArn:         c.SecretArn,
			TableName:         c.TableName,
			AwsConfig:         &awsConfig,
			AutoMigrate:       c.AutoMigrate,
		}, nil
	case config.AzureExporter:
		return &azureexporter.Exporter{
			Container:               c.Container,
			Format:                  format,
			Path:                    c.Path,
			Filename:                filename,
			CsvTemplate:             csvTemplate,
			ParquetCompressionCodec: parquetCompressionCodec,
			AccountKey:              c.AccountKey,
			AccountName:             c.AccountName,
		}, nil
	case config.OpenTelemetryExporter:
		return &opentelemetryexporter.Exporter{
			TracerName: c.TracerName,
		}, nil
	default:
		return nil, fmt.Errorf("invalid exporter: kind \"%s\" is not supported", c.Kind)
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
	err := mergo.Merge(saramaConfig, k.Config)
	if err != nil {
		return kafkaexporter.Settings{}, err
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

func initNotifier(c []config.NotifierConf, logger *zap.Logger) ([]notifier.Notifier, error) {
	if c == nil {
		return nil, nil
	}
	var notifiers []notifier.Notifier

	for _, cNotif := range c {
		switch cNotif.Kind {
		case config.SlackNotifier:
			if cNotif.WebhookURL == "" && cNotif.SlackWebhookURL != "" { // nolint
				logger.Warn("slackWebhookURL field is deprecated, please use webhookURL instead")
				cNotif.WebhookURL = cNotif.SlackWebhookURL // nolint
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
			return nil, fmt.Errorf("invalid notifier: kind \"%s\" is not supported", cNotif.Kind)
		}
	}

	return notifiers, nil
}
