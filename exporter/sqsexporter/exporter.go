package sqsexporter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/blackbirdworks/semaphore/exporter"
)

var (
	errImpossibleToInitSQS = errors.New("impossible to init SQS exporter: QueueURL is a mandatory parameter")
)

var _ exporter.Exporter = &Exporter{}

// Exporter sends evaluation events to Amazon SQS.
type Exporter struct {
	sqsService SQSSendMessageAPI
	// loadConfig loads the configuration of the default AWS chain when AwsConfig is nil. It is nil in production (then
	// config.LoadDefaultConfig) and is replaced only by tests: an exporter that is given its configuration, as the
	// relay proxy does from the connection factory, never calls it.
	loadConfig func(context.Context) (aws.Config, error)
	// AwsConfig is the AWS SDK configuration object we will use to
	// upload your exported data files.
	AwsConfig *aws.Config
	// QueueURL is the URL of your SQS queue
	// (mandatory)
	QueueURL string
	// initMu guards the initialisation, which is retried by the next call until it has succeeded.
	initMu   sync.Mutex
	initDone bool
}

// Export is sending SQS event for each events received.
func (f *Exporter) Export(
	ctx context.Context,
	_ *slog.Logger,
	events []exporter.ExportableEvent,
) error {
	initErr := f.initialize(ctx)
	if initErr != nil {
		return initErr
	}

	if f.QueueURL == "" {
		return errImpossibleToInitSQS
	}

	for _, event := range events {
		messageBody, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("marshal SQS event: %w", err)
		}

		_, err = f.sqsService.SendMessage(ctx, &sqs.SendMessageInput{
			MessageBody: aws.String(string(messageBody)),
			QueueUrl:    aws.String(f.QueueURL),
			MessageAttributes: map[string]types.MessageAttributeValue{
				"emitter": {
					DataType:    aws.String("String"),
					StringValue: aws.String("GO Feature Flag"),
				},
			},
		})
		if err != nil {
			return fmt.Errorf("send SQS message: %w", err)
		}
	}

	return nil
}

// IsBulk returns false: this exporter sends each event on its own as a SQS message, it does not batch events.
func (*Exporter) IsBulk() bool {
	return false
}

// SQSSendMessageAPI defines the interface for the GetQueueUrl and SendMessage functions.
// We use this interface to test the functions using a mocked service.
type SQSSendMessageAPI interface {
	SendMessage(ctx context.Context,
		params *sqs.SendMessageInput,
		optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// initialize loads the configuration when none was given and builds the SQS service; it is retried by the next call
// until it has succeeded.
func (f *Exporter) initialize(ctx context.Context) error {
	f.initMu.Lock()
	defer f.initMu.Unlock()

	if f.initDone {
		return nil
	}

	if f.AwsConfig == nil {
		cfg, err := f.defaultConfig(ctx)
		if err != nil {
			return fmt.Errorf("impossible to init SQS exporter: %w", err)
		}

		f.AwsConfig = &cfg
	}

	if f.sqsService == nil {
		f.sqsService = sqs.NewFromConfig(*f.AwsConfig)
	}

	f.initDone = true

	return nil
}

// defaultConfig loads the configuration of the default AWS chain; it is only reached by an exporter that is used as
// a library and has no AwsConfig.
func (f *Exporter) defaultConfig(ctx context.Context) (aws.Config, error) {
	if f.loadConfig != nil {
		return f.loadConfig(ctx)
	}

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load the default AWS configuration: %w", err)
	}

	return cfg, nil
}
